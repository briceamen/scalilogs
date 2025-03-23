package archive

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/scalilogs/pkg/scalingo"
)

// ArchiveDownloader implements an interface that can download archives
type ArchiveDownloader struct {
	ctx        context.Context
	client     *scalingo.Client
	archiveURL string
}

// NewArchiveDownloader creates a new downloader for archives
func NewArchiveDownloader(ctx context.Context, client *scalingo.Client, archiveURL string) *ArchiveDownloader {
	return &ArchiveDownloader{
		ctx:        ctx,
		client:     client,
		archiveURL: archiveURL,
	}
}

// DownloadToWriter downloads the archive content to the provided writer
func (d *ArchiveDownloader) DownloadToWriter(ctx context.Context, writer io.Writer) error {
	return scalingo.DownloadArchiveToWriter(ctx, d.archiveURL, writer)
}

// downloadArchive downloads an archive with progress tracking UI
func downloadArchive(ctx context.Context, client *scalingo.Client, url, archiveFileName string) (string, error) {

	// Get archive info including size
	_, err := scalingo.GetArchiveInfo(ctx, url, archiveFileName)
	if err != nil {
		return "", errors.Wrap(ctx, err, "get archive information")
	}

	// Create a temporary file to store the download
	tmpfile, err := os.CreateTemp("", fmt.Sprintf("logs-archive-%s-*.gz", archiveFileName))
	if err != nil {
		return "", errors.Wrap(ctx, err, "create temporary file for download")
	}
	defer tmpfile.Close()
	tmpfilePath := tmpfile.Name()

	// Create a downloader
	downloader := NewArchiveDownloader(ctx, client, url)

	// Download directly to the temp file
	err = downloader.DownloadToWriter(ctx, tmpfile)
	if err != nil {
		// Clean up the file on error
		os.Remove(tmpfilePath)
		return "", errors.Wrap(ctx, err, "download archive")
	}

	// Just make sure we have the file
	if _, err := os.Stat(tmpfilePath); os.IsNotExist(err) {
		return "", errors.Wrap(ctx, err, "temporary file not found after download")
	}

	return tmpfilePath, nil
}

// CreateDecompressedArchive downloads an archive, decompresses it, and returns the path to the decompressed file
func CreateDecompressedArchive(ctx context.Context, client *scalingo.Client, url, archiveFileName, outputDir string, index int) (string, error) {
	// Download the archive with progress tracking
	compressedFilePath, err := downloadArchive(ctx, client, url, archiveFileName)
	if err != nil {
		return "", errors.Wrap(ctx, err, "download archive with progress")
	}
	// Clean up the compressed file when done
	defer os.Remove(compressedFilePath)

	// Create output file path for the decompressed content
	outputPath := fmt.Sprintf("%s/%s-archive-%d.log", outputDir, archiveFileName, index)

	// Create the output file
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return "", errors.Wrap(ctx, err, "create output file for decompressed archive")
	}
	defer outputFile.Close()

	// Decompress the archive using the existing logic
	if err := decompressGzipFile(ctx, compressedFilePath, outputFile); err != nil {
		// Clean up the output file on error
		os.Remove(outputPath)
		return "", errors.Wrap(ctx, err, "decompress archive")
	}

	return outputPath, nil
}

// decompressGzipFile decompresses a gzip file to the provided writer
func decompressGzipFile(ctx context.Context, gzipFilePath string, output io.Writer) error {
	return executeCommand(ctx, "zcat", []string{gzipFilePath}, nil, output)
}

// executeCommand runs a command with the given input and output
func executeCommand(ctx context.Context, command string, args []string, input io.Reader, output io.Writer) error {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdin = input
	cmd.Stdout = output

	if err := cmd.Run(); err != nil {
		return errors.Wrap(ctx, err, fmt.Sprintf("execute command: %s", command))
	}

	return nil
}
