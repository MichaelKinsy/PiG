### Fixed

- Model Runtime passes cancellation to injected credential stores during Radius catalog restoration and request authentication. Cancelled refreshes return without waiting for an uncooperative store, while runtime shutdown drains retained reads, including native provider reads. Storage failures reach the caller instead of falling through to ambient Radius credentials. A concurrent OAuth refresh leaves an already refreshed credential unchanged and uses the store's post-modify result.
