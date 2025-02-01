# Bot Restart Functionality

This document describes the automatic bot restart functionality implemented in the msgmate package.

## Overview

The bot restart functionality provides automatic recovery from bot crashes with the following features:

- **Automatic Restart**: When the bot crashes, it automatically restarts after a configurable delay
- **Error Logging**: All crashes are logged to disk with detailed information
- **Exponential Backoff**: Restart delays increase with each failure to prevent overwhelming the system
- **Context Cancellation**: Support for graceful shutdown via context cancellation
- **Panic Recovery**: Protection against panics in the restart loop itself

## Functions

### `StartBotWithRestart(host, ch, username, password)`

The main function to start the bot with restart capability. This is a convenience wrapper around `StartBotWithRestartContext`.

### `StartBotWithRestartContext(ctx, host, ch, username, password)`

The full-featured restart function that accepts a context for cancellation control.

## Configuration

The restart behavior is configured with the following parameters:

- **Base Restart Delay**: 5 seconds (initial delay between restarts)
- **Max Restart Delay**: 30 seconds (maximum delay between restarts)
- **Max Restart Attempts**: 1000 (prevents infinite restarts)

## Error Logging

Bot crashes are logged to the `logs/` directory with the following format:

```
[2024-01-15 10:30:45] Bot crash (attempt 3) for user 'bot':
  Error: connection refused
  Timestamp: 2024-01-15T10:30:45Z
  Attempt: 3
  User: bot
  --------------------------------------------------
```

## Usage

The bot restart functionality is automatically enabled when the `--start-bot` flag is set to true in the server configuration.

## Log Files

Error logs are written to timestamped files in the `logs/` directory:
- Format: `bot_errors_YYYY-MM-DD_HH-MM-SS.log`
- Location: `logs/bot_errors_*.log`

## Safety Features

1. **Panic Recovery**: The restart loop is protected against panics
2. **Max Attempts**: Prevents infinite restart loops
3. **Context Cancellation**: Allows graceful shutdown
4. **Exponential Backoff**: Prevents overwhelming the system during failures
5. **Detailed Logging**: Comprehensive error information for debugging

## Example Log Output

```
Starting bot (attempt 1)...
Bot connected to WebSocket
Error reading from WebSocket: connection lost
Bot crashed (attempt 1): connection lost
Restarting bot in 5s (attempt 2)
Starting bot (attempt 2)...
Bot connected to WebSocket
...
``` 