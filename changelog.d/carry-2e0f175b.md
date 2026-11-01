### Fixed

- The model catalog no longer offers models.dev's realtime and image models as chat models: `gpt-realtime-2.1` (Azure and OpenAI), `gemini-3.1-flash-live-preview` and `gemini-3.1-flash-lite-image` (Google and Google Vertex). Carried forward, as the row removal only, from Pi 2e0f175b (earendil-works/pi#11368), past the 1.4.0+b3a29f11 pin. <!-- fix: 2e0f175b -->
