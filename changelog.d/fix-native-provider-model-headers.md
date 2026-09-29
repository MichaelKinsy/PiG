### Fixed

- Send extension model headers, models.json `modelOverrides` headers and `$VAR` or `!command` header values with requests to providers registered through `RegisterProvider` or `RegisterNativeProvider`, and keep a header that `TransformHeaders` deletes out of the request. Native providers now resolve model auth through the same registry path as other providers.
