# wikr

wikr is a simple command line tool that provides quick summaries of Wikipedia articles in German or English.

## Features

- Search for Wikipedia articles
- Display article summaries directly in the console
- Support for German and English Wikipedia
- Caching of search results for faster access
- Interactive selection for multiple search results

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

## Usage

```shell
wikr [en|de] search term
```

- `en` or `de` (optional): Selects the language (english or german). Default is english.
- `-lang`: set search language to `en` or `de`
- `search term`: The term or article title to search for.
- `-max`: The maximum number of results to display. Default is 5.
- `-clearcache`: Clears the cache.
- `-version`: Shows version.

### Examples

```shell
wikr Eiffelturm
wikr en Einstein
wike -lang de Aeinstein
wikr -max 3 Eiffelturm
wikr -clearcache
wikr -version
```

## Cache

Wikr stores search results in a cache file (`.wikr_cache.json`) in the user's home directory. The cache is valid for 24 hours.

## Dependencies

- [github.com/fatih/color](https://github.com/fatih/color) for colored console output

## License

[MIT License](LICENSE)

## Contributes

Contributes are welcome! Please open an Issue or a Pull Request for suggestions or bug fixes.
