** General Purpose **
It searches the web using a SearXNG instance (self-hosted metasearch engine) and returns results with title, URL, and description.

** Input **
- query: (required) The search query string.
- numResults: (optional, default 10, max 20) Number of results to return.

** Output **
A JSON array of objects, where each object represents a search result with the following fields:
- title: the title of the web page.
- url: the URL of the web page.
- description: a short snippet describing the page content.

** Notes **
- This tool requires a SearXNG instance (see https://docs.searxng.org). Set SearxngURL in the config (e.g. "https://searxng.example.com").
- SearXNG aggregates results from Google, Bing, Brave, Wikipedia, and other engines.
- The tool retries up to 3 times with exponential backoff on transient errors.
