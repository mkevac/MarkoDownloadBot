# Marko Download Bot

This is a Telegram bot that receives URL to a video for popular sites like Instagram, TikTok, YouTube, etc. and responds with a downloaded video. It is convenient for sharing content with your friends and family.

![GIF](MarkoDownloadBot.gif)

## Installation

1. Create a new bot using Bot Father (https://t.me/BotFather). Remember your token
2. Acquire API ID and Hash from https://my.telegram.org/apps. Remember them
3. Clone this repository: `git clone https://github.com/marko/MarkoDownloadBot.git`
4. Navigate to the project directory: `cd MarkoDownloadBot`
5. Create file `.env` with this content:
```
TELEGRAM_API_ID=<api_id>
TELEGRAM_API_HASH=<api_hash>
TELEGRAM_BOT_API_TOKEN=<bot_token>
ADMIN_USERNAME=<your_telegram_username>
COOKIES_FILE=/path/to/your/cookies.txt
```
6. Run the bot using docker compose: `docker compose up -d`
7. Write `/start` to your new Telegram bot

## Usage

### User Commands

1. **Video Download**: Simply send a URL to the bot, and it will download and send the video to you.

2. `/audio [URL]`: Use this command followed by an audio URL to download and receive audio files.

3. `/help` or `/start`: Displays a help message with information about how to use the bot.

### Admin Commands

The following commands are available only to the admin user specified in `ADMIN_USERNAME`:

1. `/stats`: Provides detailed usage statistics of the bot, including video/audio requests, errors, and top users.

2. `/users`: Shows the total number of registered active users.

3. `/broadcast <message>`: Sends a message to all active bot users. Example:
   ```
   /broadcast Hello! The bot will be down for maintenance at 10 PM.
   ```

   The broadcast feature automatically:
   - Tracks which users have blocked the bot
   - Marks blocked users as inactive
   - Reactivates users when they return
   - Only sends messages to active users

To download media, just send a valid video or audio link to the bot, and it will handle the rest!

## User Management

The bot automatically tracks all users who interact with it, storing their:
- Chat ID
- Username
- First and last name
- Active status
- Last interaction timestamp

When users block the bot, they are automatically marked as inactive and won't receive future broadcasts. If they return and interact with the bot again, they are automatically reactivated.

## Custom Cookies File

To use a custom cookies file with yt-dlp:

1. Create a cookies file (e.g., `cookies.txt`) with the necessary cookies.
2. Add the following line to your `.env` file:
   ```
   COOKIES_FILE=/path/to/your/cookies.txt
   ```
3. Restart the bot using `docker compose up -d`

If no custom cookies file is specified, an empty cookies file will be used by default.

## Contributing

Contributions are welcome! If you have any ideas or improvements, feel free to submit a pull request.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for more information.
