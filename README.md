# Scalilogs

A command-line utility for extracting and filtering logs from applications hosted on Scalingo around a specific timestamp.

## Features

- Extract all logs (recent and archived) around a specific timestamp
- Define the quantity of logs to extract either by line count or hours
- Support for multiple environments (production, staging, development) and regions
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
scalilogs

# Run with command-line flags (filtering by lines around timestamp)
scalilogs -a app-name -t "2023-06-15 14:30:00" -l 100

# Run with command-line flags (filtering by hours around timestamp)
scalilogs -a app-name -t "2023-06-15 14:30:00" -h 2

# Run with a specific region
scalilogs -a app-name -t "2023-06-15 14:30:00" -l 100 -r osc-secnum-fr1

# Run with a specific environment
scalilogs -a app-name -t "2023-06-15 14:30:00" -l 100 -e staging

# Long-form flags with environment and region
scalilogs --app my-app --timestamp "2023-06-15 14:30:00" --hours 3 --env production --region osc-fr1

# With relative timestamp (will be validated and normalized)
scalilogs -a my-app -t "Today at 14:30:00" -l 100
```

### Interactive Mode

When run without flags, the tool will guide you through the log extraction process with prompts for:

1. Application name
2. Environment selection (Production, Staging, Development)
3. Region selection (fetched dynamically from the Scalingo API)
4. Timestamp (with flexible format support)
5. Filtering method (line count or hours)
6. Number of lines or hours to show before and after the timestamp

### Timestamp Format Support

The tool supports various timestamp formats:

- **Absolute date**: 
  - `2023-06-15 14:30:00`
  - `2023/06/15 14:30:00`
  - `2023/06/15 14:30`
  - `15/06/2023 14:30:00` (DD/MM/YYYY)
  - `06/15/2023 14:30:00` (MM/DD/YYYY)
  - `2023-06-15T14:30:00` (ISO 8601)
  - `2023-06-15 14:30`

- **With 'at'**:
  - `2025-03-22 at 12:00:00`
  - `2025-03-22 at 12:00`
  - `2025-03-22 at 12`

- **Relative day**:
  - `now` (current date and time)
  - `today` (defaults to noon today)
  - `today at 14:30`
  - `yesterday` (defaults to noon yesterday)
  - `yesterday at 14:30`

- **Weekday**:
  - `monday` (defaults to noon on the most recent Monday)
  - `monday at 14:30`

- **Time-only** (defaults to today):
  - `14` (interpreted as today at 14:00:00)

All formats are automatically normalized to the standard `YYYY-MM-DD HH:MM:SS` format.

### Build and Install

```bash
# Install directly using Go
go install github.com/briceamen/scalilogs@latest

# Or build from source:
# Clone the repository
git clone https://github.com/briceamen/scalilogs.git
cd scalilogs

# Build only
make

# Build and install to your Go bin path
make install
```

## Output

Logs are extracted to a temporary directory (`$TMPDIR/scalingo-logs/`) with a timestamped filename. The tool will display the path to the output file when finished.

### Environments

The tool supports three environments:

- **production**: The default environment for production applications (default)
- **staging**: For applications in the staging environment
- **dev**: For development/local environment

Each environment uses different authentication endpoints and API URLs. When using the interactive mode, you'll be prompted to select an environment. For command-line usage, you can specify the environment with the `-e` or `--env` flag.