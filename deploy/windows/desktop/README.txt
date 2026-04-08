Cloud Relay Desktop Console Portable

1. Edit desktop-config.json before first launch.
2. Set apiBaseUrl to your server-api base URL, for example http://82.156.236.104:7710.
3. Optionally set publicEntryHost to the public relay host shown in workbench entries.
4. Start desktop-launcher.exe.
5. Open http://127.0.0.1:5180 in your browser.

Bundled files:
- desktop-launcher.exe: serves the desktop UI and proxies /api to server-api
- desktop-dist/: static desktop-console build output
- client-agent.exe: Windows client agent runtime
- desktop-config.json: runtime config created from the example file
- logs/desktop-launcher.log: launcher logs

Current scope:
- HTTP / HTTPS / UDP / SOCKS5 / TCP workbench and control UI
- P2P remains partial / non-blocking
