# wikr

wikr is a simple command line tool that provides quick summaries of Wikipedia articles in English or German.

Current version: `0.8.2`

## Features

- Search Wikipedia (English or German)
- Search Grokipedia (English) as an alternative source
- Interactive selection when multiple matches are returned
- Summary extraction with link to full article
- Two-layer caching:
  - Search results (titles list)
  - Article summaries (content + URL)
- Config persistence (language, source + max results) with auto-correction on invalid values
- Graceful offline/network failure fallback (uses cached search if available)
- `-reset-config` to restore defaults quickly

## Dependencies

- **Go**: Required for building and installing wikr.
- **Google Chrome or Chromium**: Required for Grokipedia search functionality. The `grokipedia` source uses browser automation (via chromedp) to render JavaScript-based search results. If Chrome/Chromium is not installed, Grokipedia searches will fail with an error message.

## Installation

1. Ensure that Go is installed on your system.
2. (Optional) Install Google Chrome or Chromium if you want to use the `-source grokipedia` feature.
3. Clone this Repository:

   ```shell
   git clone https://github.com/SvenSchneiderDVAG/wikr.git
   ```

4. Navigate to the project directory:

   ```shell
   cd wikr
   ```

5. Build the program:

   ```shell
   go build
   ```

6. Install the program:

   ```shell
   go install
   ```

## Smaller Binary (Optional)

Use stripped build flags to reduce binary size:

```shell
go build -ldflags="-s -w" -o wikr .
go install -ldflags="-s -w" .
```

## Usage

```shell
wikr [options] search term
```

### Options

| Flag            | Description                                                     |
| --------------- | --------------------------------------------------------------- |
| `-lang`         | Language (`en` or `de`), default from config (initially `en`).  |
| `-source`       | Content source (`wikipedia` or `grokipedia`), default from config (initially `wikipedia`). |
| `-max`          | Max number of listed results (default 5, persisted).            |
| `-clear-cache`  | Clears summary cache only (search cache persists separately).   |
| `-reset-config` | Regenerates config file with defaults (`en`, `wikipedia`, `5`) and exits. |
| `-version`      | Prints the version and exits.                                   |

Changing `-lang`, `-source` or `-max` updates the persisted config automatically. Invalid stored values are silently corrected and re-saved.

### Examples

```shell
wikr golang
wikr -lang de golang
wikr -source grokipedia golang
wikr -max 3 golang
wikr -clear-cache
wikr -reset-config
wikr -version
```

## Cache

Two caches (24h TTL each):

- Summary cache: `cache.json` (per (lang:title) with summary + URL)
- Search cache: `search_cache.json` (per (lang:query) with returned titles)

If a network error occurs during search, a cached result (if present and still valid) is used and a warning is shown. Summaries are only fetched if not already cached.


## License

[MIT License](LICENSE)

## Contributing

Contributions are welcome. Feel free to open an issue or PR for enhancements, bug reports, offline mode ideas, or performance improvements.
