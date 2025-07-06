# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Commands

### Building and Running
- `make build` - Build the Go binary with CGO disabled
- `make run` - Start services using docker-compose (production)
- `make debug` - Start API service and run locally (development)
- `make run-local` - Build and run locally with IS_LOCAL=true
- `make stop` - Stop docker-compose services

### Docker Operations
- `make docker` - Build Docker image locally (uses git tags for versioning)
- `make push` - Build and push multi-platform Docker images

### Local Development Setup
1. Create `.env` file with required environment variables (see README.md)
2. Run `make debug` to start telegram-bot-api service and run bot locally
3. Use `IS_LOCAL=true` environment variable for local development mode

## Architecture Overview

### Core Components

**Main Application** (`main.go`)
- Telegram bot using `github.com/go-telegram/bot` library
- HTTP file server on port 8080 for serving downloaded media
- Admin authentication system via username
- Signal handling for graceful shutdown

**Media Processing** (`video.go`)
- **Intelligent Video Conversion System**: Sophisticated analysis with smart stream selection and modern codec handling
- **MediaAnalysis struct**: Uses ffprobe to analyze video properties and determine optimal conversion strategy
- **Smart Multi-Stream Selection**: Intelligently selects best video/audio streams based on quality and disposition flags
- **Modern Conversion Logic**:
  - H.265 for all video conversions (superior compression efficiency)
  - HEVC compatibility (preserved as-is, no unnecessary conversion)
  - Selective conversion: only AV1/VP9 codecs that lack mobile support
  - Audio codec detection (copies AAC/MP3, converts Opus/Vorbis/FLAC)
  - File size optimization targeting 110-120% of original
- **Dual Download Strategy**: Primary attempt with format restrictions, fallback with simplified parameters
- **Platform-specific Logic**: YouTube, TikTok, and general URL handling

**Statistics System** (`stats/`)
- SQLite database for tracking usage metrics
- Per-user tracking of video requests, audio requests, errors, and unrecognized commands
- Time-based statistics (day, week, month, overall)

### Key Dependencies
- **yt-dlp**: Primary media download tool (external dependency)
- **ffmpeg**: Video conversion and analysis (external dependency, includes ffprobe)
- **SQLite**: Statistics storage via `modernc.org/sqlite`
- **Telegram Bot API**: Local API server via Docker

### Environment Configuration
- Uses docker-compose with local Telegram Bot API server
- Environment variables loaded via `.env` file
- Cookies file support for yt-dlp authentication
- Separate local development mode

### Media Processing Workflow
1. URL validation and parsing
2. yt-dlp download with platform-specific parameters
3. Metadata extraction from JSON output
4. **Multi-Stream Analysis**: Smart selection of best video/audio streams from available options
5. **Intelligent Analysis**: ffprobe analysis to determine conversion needs for mobile compatibility
6. **Modern Conversion**: H.265 with calculated bitrates for optimal compression, or skip if already compatible
7. File serving via HTTP endpoint
8. Cleanup of temporary files

### Admin Features
- Username-based admin authentication
- Comprehensive statistics via `/stats` command
- Error reporting to admin chat
- Access control for sensitive commands

## Video Conversion System

The bot features a sophisticated video conversion system that:
- **Smart Multi-Stream Selection**: Chooses best video/audio streams based on quality, resolution, channels, and disposition flags
- **Selective Codec Analysis**: Only converts problematic codecs (AV1, VP9) that lack mobile browser support
- **Modern H.265 Conversion**: Uses H.265 exclusively for superior compression efficiency over outdated H.264
- **HEVC Preservation**: Keeps HEVC files as-is since they're well supported on modern iOS devices
- **Intelligent Audio Handling**: Copies compatible codecs (AAC, MP3), converts problematic ones (Opus, Vorbis, FLAC)
- **Optimized File Sizes**: Calculates target bitrates based on original file size and duration for 110-120% size ratio
- **Graceful Degradation**: Skips conversion and serves original file if analysis fails

### Codec Compatibility Strategy
- **Preserve**: HEVC, H.264, AAC, MP3 (already mobile-compatible)
- **Convert to H.265**: AV1, VP9 (poor mobile hardware support, battery drain)
- **Convert to AAC**: Opus (Safari incompatible), Vorbis (limited mobile support), FLAC (large files)

When working with video processing code, understand that the system prioritizes modern codec efficiency while ensuring mobile compatibility. The system has moved away from legacy H.264 conversion in favor of H.265-only strategy for better compression.