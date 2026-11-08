### Added

- `ai.Models.WrapModelAuth` lets a host that composes configuration over a models collection it did not create wrap the model-level auth resolution (the callback given at creation, else the provider's auth with the model's own headers merged in), the Go form of an application overriding `getAuth` on a Pi `Models` instance. It is safe to call while requests resolve auth. <!-- feat: p2-wrap-model-auth -->
