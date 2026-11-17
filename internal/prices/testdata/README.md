# Price source fixtures

Every file here is hand-written in the shape each source's public
documentation describes. The prices, volumes and dates are made up; none of
it was downloaded from a source.

- `tiingo_daily.json`: Tiingo's end-of-day prices array, with a two-for-one
  split taking effect on the last date.
- `tiingo_not_found.json`: the body Tiingo sends with a 404 for an unknown
  ticker.
- `stooq_daily.csv`: Stooq's daily CSV download.
- `stooq_no_data.txt`: Stooq's answer for an unknown symbol or empty range.
- `stooq_limit.txt`: Stooq's answer once the daily request limit is reached.
- `stooq_no_key.html`: a stand-in for the web page Stooq sends instead of CSV
  when the API key is missing or wrong.
