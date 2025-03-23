package logs

import (
	"context"

	"github.com/briceamen/scalilogs/internal/status"
	"github.com/briceamen/scalilogs/internal/tui"
	"github.com/briceamen/scalilogs/pkg/scalingo"
)

// ExtractLogs extracts logs for the specified app around a target timestamp
func ExtractLogs(ctx context.Context, client *scalingo.Client, appName, targetTimestamp string, lineCount int, hoursCount int) (string, error) {
	var outputFilePath string

	extractFunc := func(statusCh chan status.Message, finishCh chan status.FinishMessage) error {
		// Update status
		status.Update(statusCh, "initializing log extraction")

		// Recreate the client with statusCh to capture token exchange and region selection logs
		recreatedClient, err := scalingo.NewScalingoClient(ctx, client.Env, client.Region, statusCh)
		if err != nil {
			// Report error to the status channel without additional wrapping
			status.ReportError(ctx, statusCh, err)
			return err
		}
		client = recreatedClient

		// Create config for log search
		config := LogSearchConfig{
			AppName:         appName,
			TargetTimestamp: targetTimestamp,
			LineCount:       lineCount,
			HoursCount:      hoursCount,
		}

		result, err := SearchLogs(ctx, client, config, statusCh)
		if err != nil {
			status.ReportError(ctx, statusCh, err)
			return err
		}

		// Send finish message with all details including timing information
		tui.FinishProcess(finishCh, result.OutputFile, result.LiveLogsCount, result.ArchiveLogsCount,
			result.TotalLines, result.FilteredLines, result.ArchiveDetails, result.ElapsedTime,
			result.ArchiveSelectionTime, result.FetchLiveTime, result.FetchArchiveTime,
			result.SortTime, result.FilterTime)

		outputFilePath = result.OutputFile
		return nil
	}

	if err := tui.RunExtractor(ctx, appName, targetTimestamp, extractFunc); err != nil {
		return "", err
	}

	return outputFilePath, nil
}
