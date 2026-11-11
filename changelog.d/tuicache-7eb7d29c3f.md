### Fixed

- The model picker (`/model`) now shows a catalog-refresh timeout or failure as soon as it happens. It kept showing `Refreshing model catalogs...` until the next key press, because the screen reused the picker's last drawing; `SettingsList` value updates and `FilterableList` cursor moves had the same hole. A component that embeds a container now tells its parent about a change anywhere beneath it, so the per-child render cache can no longer show a child that Pi would redraw. <!-- fix: 7eb7d29c3f0ed478db4e88cfa8a627d6c8b58732 -->
