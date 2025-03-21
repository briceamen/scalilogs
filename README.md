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

### Command-Line Mode

For scripting or automation, you can use these flags:

```bash
scalilogs [OPTIONS]

Options:
  -a, --app string       App name
  -t, --timestamp string Timestamp (format: YYYY-MM-DD HH:MM:SS)
  -l, --lines int        Number of lines before and after timestamp (default: 100)
  -h, --hours int        Number of hours before and after timestamp
  -e, --env string       Environment (production/staging/dev, default: production)
  -r, --region string    Region (e.g., osc-fr1, osc-secnum-fr1)
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

### Regions

Regions are data centers where your Scalingo applications are hosted. The tool automatically fetches available regions from the Scalingo API based on your selected environment:

- Common production regions: `osc-fr1` (Paris - Outscale), `osc-secnum-fr1` (Paris - SecNumCloud)
- If no region is specified, the tool will use the first available region for your environment

