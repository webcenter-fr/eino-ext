** General Purpose **
It fetches the content of a web page at the given URL and returns it in the requested format.

** Input **
- url: (required) The URL to fetch. Must start with http:// or https://.
- format: (optional, default "markdown") The output format. One of: "markdown", "text", "html".
- timeout: (optional, default 30, max 120) Request timeout in seconds.

** Output **
The raw content of the page as a string in the requested format:
- "markdown": HTML is converted to Markdown for readability.
- "text": HTML tags are stripped, leaving only plain text.
- "html": The raw HTML content is returned as-is.

** Notes **
- URLs are validated against SSRF attacks (private IPs, loopback, link-local addresses are blocked).
- Response bodies are capped at 5MB.
- The tool retries on certain transient errors with a different User-Agent.
