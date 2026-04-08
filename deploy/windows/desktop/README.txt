Cloud Relay Desktop Console Portable

1. Edit desktop-config.json before first launch.
2. Set apiBaseUrl to your upstream server-api base URL, for example http://82.156.236.104:7710. The desktop page itself stays on the local launcher origin and reaches the server through the launcher's /api reverse proxy.
3. Optionally set publicEntryHost to the public relay host shown in workbench entries.
4. Start desktop-launcher.exe. With openBrowser=true, the launcher will prefer Microsoft Edge or Google Chrome on Windows before falling back to the system default browser.
5. If the page still opens in an old browser, manually open http://127.0.0.1:5180/ in Edge or Chrome. Legacy browsers will only show a compatibility notice.

Bundled files:
- desktop-launcher.exe: serves the desktop UI locally, proxies /api to upstream server-api, and can auto-open the local UI in a modern browser
- desktop-dist/: static desktop-console build output
- client-agent.exe: Windows client agent runtime
- desktop-config.json: runtime config created from the example file
- logs/desktop-launcher.log: launcher logs

Current scope:
- HTTP / HTTPS / UDP / SOCKS5 / TCP workbench and control UI
- P2P remains partial / non-blocking
