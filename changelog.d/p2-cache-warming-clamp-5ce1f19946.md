### Fixed

- p2's cache warming no longer sends repeated refresh requests for a `promptCache` lifetime beyond about 27.6 days (a refresh delay beyond Node's 2147483647 ms timer limit, about 24.8 days): Pi re-arms such a refresh after 1 ms and sends paid one-token requests back to back until its warming window closes (60 minutes while a run is streaming, 30 minutes once idle); PiG arms the timer for the limit, then stops without a request (D612). <!-- fix: 5ce1f19946 -->
