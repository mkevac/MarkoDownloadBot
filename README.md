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

The bot supports the following commands:

1. **Video Download**: Simply send a URL to the bot, and it will download and send the video to you.

2. `/audio [URL]`: Use this command followed by an audio URL to download and receive audio files.

3. `/stats`: (Admin only) Provides basic usage statistics of the bot.

4. `/help` or `/start`: Displays a help message with information about how to use the bot.

To download media, just send a valid video or audio link to the bot, and it will handle the rest!

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
