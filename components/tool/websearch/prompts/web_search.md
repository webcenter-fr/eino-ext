** General Purpose **
It searches the web using DuckDuckGo HTML (no API key required) and returns results with title, URL, and description.

** Input **
- query: (required) The search query string.
- numResults: (optional, default 10, max 20) Number of results to return.

** Output **
A JSON array of objects, where each object represents a search result with the following fields:
- title: the title of the web page.
- url: the URL of the web page.
- description: a short snippet describing the page content.

** Notes **
- This tool uses DuckDuckGo's HTML search interface (html.duckduckgo.com) with a fallback to the lite version.
- Results may vary based on DuckDuckGo's availability and rate limiting.
- The tool retries up to 3 times with exponential backoff on transient errors.
