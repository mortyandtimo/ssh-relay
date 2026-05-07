# Tauri Host Scaffold

This directory is reserved for the Windows desktop shell layer.

Target runtime:
- Tauri 2
- Windows 10 / Windows 11
- MSI first, portable zip second

Planned host responsibilities:
- tray
- window lifecycle
- config directory
- log directory
- native notifications
- minimize-to-tray on close
- autostart placeholder
- updater placeholder

Current status:
- product-layer UI has been rebuilt toward the publisher workflow
- Rust / Cargo is not available in the current environment yet
- this scaffold is added now so the next phase can wire the host without re-deciding the layout
