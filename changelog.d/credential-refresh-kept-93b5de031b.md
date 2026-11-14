### Fixed

- A login refresh that took between ten and fifteen seconds while another pig process opened the credential file was thrown away, and the next request asked you to log in again. pig now saves the refreshed login for that provider into the file as it is at that moment, leaving every other provider's entry as the other process wrote it. <!-- fix: 93b5de031b --> <!-- cause: Modify treated a lock taken over while the refresh ran as a failure of the whole write, though the refreshed credential, possibly the only copy of a single-use refresh token, was already in hand. -->
