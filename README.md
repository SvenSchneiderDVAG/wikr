# wikr

wikr is a simple command line tool that provides quick summaries of Wikipedia articles in English or German.

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

## Installation

1. Ensure that Go is installed on your system.
2. Clone this Repository:

   ```shell
   git clone https://github.com/SvenSchneiderDVAG/wikr.git
   ```

3. Navigate to the project directory:

   ```shell
   cd wikr
   ```

4. Build the program:

   ```shell
   go build
   ```

5. Install the program:

   ```shell
   go install
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

## Dependencies

- [github.com/fatih/color](https://github.com/fatih/color) for colored console output

## License

[MIT License](LICENSE)

## Contributing

Contributions are welcome. Feel free to open an issue or PR for enhancements, bug reports, offline mode ideas, or performance improvements.
