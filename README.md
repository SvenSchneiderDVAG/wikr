# wikr

wikr is a simple command line tool that provides quick summaries of Wikipedia articles in English or German.

## Features

- Search for Wikipedia articles
- Display article summaries directly in the console
- Adds a link to the full article
- Supports English and German Wikipedia
- Interactive selection for multiple search results
- Caching of search results for faster access

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

- `-lang` set search language to `en` or `de`, defaults to `en`
- `-max` The maximum number of results to display, defaults to 5
- `-clearcache` Clears the cache
- `-version` Shows version

When you set a language or maximum number of results, it will be saved in a config file for future use.

### Examples

```shell
wikr Eiffelturm
wikr -lang de Einstein
wikr -max 3 Eiffelturm
wikr -clearcache
wikr -version
```

## Cache

Wikr stores search results in a cache file (`.wikr_cache.json`). The cache is valid for 24 hours.

## Dependencies

- [github.com/fatih/color](https://github.com/fatih/color) for colored console output

## License

[MIT License](LICENSE)

## Contributes

Contributes are welcome! Please open an issue or a pull request for suggestions or bug fixes.
