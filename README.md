# Logaround

A command-line utility for extracting and filtering logs from applications hosted on Scalingo around a specific timestamp.

## Features

- Extract all logs (recent and archived) around a specific timestamp
- Define the quantity of logs to extract either by line count or hours
- Interactive survey or inline mode with command-line flags
- Flexible timestamp input formats
- Sorted and filtered results for easier troubleshooting

## Requirements

- Go 1.23 or higher
- Scalingo CLI configured and authenticated
- Access to one application hosted on Scalingo


## Usage

The tool can be used in interactive mode or with command-line flags.

```bash
# Run in interactive mode
logaround

# Run with command-line flags (filtering by lines around timestamp)
logaround -a app-name -t "2023-06-15 14:30:00" -l 100

# Run with command-line flags (filtering by hours around timestamp)
logaround -a app-name -t "2023-06-15 14:30:00" -h 2

# Long-form flags
logaround --app my-app --timestamp "2023-06-15 14:30:00" --hours 3

# With relative timestamp (will be validated and normalized)
logaround -a my-app -t "Today at 14:30:00" -l 100
```

### Interactive Mode

When run without flags, the tool will guide you through the log extraction process with prompts for:

1. Application name
2. Timestamp (with flexible format support)
3. Filtering method (line count or hours)
4. Number of lines or hours to show before and after the timestamp

### Command-Line Mode

For scripting or automation, you can use these flags:

```bash
logaround [OPTIONS]

Options:
  -a, --app string       App name
  -t, --timestamp string Timestamp (format: YYYY-MM-DD HH:MM:SS)
  -l, --lines int        Number of lines before and after timestamp (default: 100)
  -h, --hours int        Number of hours before and after timestamp
```

### Timestamp Format Support

The tool supports various timestamp formats:

- YYYY-MM-DD HH:MM:SS (e.g., 2023-06-15 14:30:00)
- Today at HH:MM:SS (e.g., Today at 14:30:00)
- Yesterday at HH:MM:SS (e.g., Yesterday at 14:30:00)
- Monday/Tuesday/etc. at HH:MM:SS (e.g., Monday at 14:30:00)

### Build and Install

```bash
# Install directly using Go
go install github.com/briceamen/logaround@latest

# Or build from source:
# Clone the repository
git clone https://github.com/briceamen/logaround.git
cd logaround

# Build only
make

# Build and install to your Go bin path
make install
```

## Output

Logs are extracted to a temporary directory (`$TMPDIR/scalingo-logs/`) with a timestamped filename. The tool will display the path to the output file when finished.

