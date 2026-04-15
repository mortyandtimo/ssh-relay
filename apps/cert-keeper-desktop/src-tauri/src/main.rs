#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::collections::HashMap;
use std::fs;
use std::path::PathBuf;
use std::sync::Mutex;
use std::time::Duration;

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use reqwest::blocking::Client;
use reqwest::header::{HeaderMap, HeaderName, HeaderValue};
use reqwest::Method;
use serde::{Deserialize, Serialize};
use tauri::image::Image;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{
    AppHandle, Emitter, Manager, State, WebviewUrl, WebviewWindow, WebviewWindowBuilder, Window,
};

const INSTALLER_QUIT_ARG: &str = "--quit-for-install";

struct AppHttpState {
    client: Client,
}

impl AppHttpState {
    fn new() -> Result<Self, String> {
        let client = Client::builder()
            .redirect(reqwest::redirect::Policy::limited(10))
            .timeout(Duration::from_secs(120))
            .build()
            .map_err(|e| e.to_string())?;
        Ok(Self { client })
    }
}

#[derive(Default)]
struct WindowState {
    bounds: Mutex<Option<WindowBounds>>,
}

#[derive(Debug, Deserialize)]
struct HttpInput {
    url: String,
    method: Option<String>,
    headers: Option<HashMap<String, String>>,
    body: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct HttpOutput {
    status: u16,
    ok: bool,
    body: String,
    set_cookies: Vec<String>,
}

#[derive(Clone, Serialize, Deserialize, Default)]
struct WindowBounds {
    x: i32,
    y: i32,
    width: u32,
    height: u32,
    maximized: bool,
}

#[derive(Serialize, Deserialize, Default)]
struct AppConfig {
    #[serde(rename = "apiBaseUrl")]
    api_base_url: Option<String>,
    #[serde(rename = "closeAction")]
    close_action: Option<String>,
}

#[derive(Clone, Serialize, Deserialize)]
struct LoginProfile {
    email: String,
    #[serde(rename = "encryptedPassword")]
    encrypted_password: String,
    #[serde(rename = "autoLogin")]
    auto_login: bool,
}

#[derive(Serialize, Deserialize, Default)]
struct LoginProfilesFile {
    profiles: Vec<LoginProfile>,
    #[serde(rename = "lastUsedEmail")]
    last_used_email: Option<String>,
}

#[cfg(target_os = "windows")]
extern "system" {
    fn LocalFree(h_mem: *mut core::ffi::c_void) -> *mut core::ffi::c_void;
}

#[cfg(target_os = "windows")]
fn dpapi_encrypt(data: &[u8]) -> Result<Vec<u8>, String> {
    use windows_sys::Win32::Security::Cryptography::{CryptProtectData, CRYPT_INTEGER_BLOB};
    let mut input = CRYPT_INTEGER_BLOB {
        cbData: data.len() as u32,
        pbData: data.as_ptr() as *mut u8,
    };
    let mut output = CRYPT_INTEGER_BLOB {
        cbData: 0,
        pbData: std::ptr::null_mut(),
    };
    let result = unsafe {
        CryptProtectData(
            &mut input,
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            0,
            &mut output,
        )
    };
    if result == 0 {
        return Err("CryptProtectData failed".to_string());
    }
    let encrypted =
        unsafe { std::slice::from_raw_parts(output.pbData, output.cbData as usize).to_vec() };
    unsafe {
        LocalFree(output.pbData as *mut _);
    }
    Ok(encrypted)
}

#[cfg(target_os = "windows")]
fn dpapi_decrypt(data: &[u8]) -> Result<Vec<u8>, String> {
    use windows_sys::Win32::Security::Cryptography::{CryptUnprotectData, CRYPT_INTEGER_BLOB};
    let mut input = CRYPT_INTEGER_BLOB {
        cbData: data.len() as u32,
        pbData: data.as_ptr() as *mut u8,
    };
    let mut output = CRYPT_INTEGER_BLOB {
        cbData: 0,
        pbData: std::ptr::null_mut(),
    };
    let result = unsafe {
        CryptUnprotectData(
            &mut input,
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            0,
            &mut output,
        )
    };
    if result == 0 {
        return Err("CryptUnprotectData failed".to_string());
    }
    let decrypted =
        unsafe { std::slice::from_raw_parts(output.pbData, output.cbData as usize).to_vec() };
    unsafe {
        LocalFree(output.pbData as *mut _);
    }
    Ok(decrypted)
}

// ─── Commands ───

#[tauri::command]
fn http_request(state: State<'_, AppHttpState>, input: HttpInput) -> Result<HttpOutput, String> {
    let method = input
        .method
        .as_deref()
        .unwrap_or("GET")
        .parse::<Method>()
        .map_err(|e| e.to_string())?;
    let mut req = state.client.request(method, &input.url);
    if let Some(headers) = input.headers {
        let mut hm = HeaderMap::new();
        for (k, v) in headers {
            let name = HeaderName::from_bytes(k.as_bytes()).map_err(|e| e.to_string())?;
            let val = HeaderValue::from_str(&v).map_err(|e| e.to_string())?;
            hm.insert(name, val);
        }
        req = req.headers(hm);
    }
    if let Some(body) = input.body {
        req = req.body(body);
    }
    let resp = req.send().map_err(|e| e.to_string())?;
    let status = resp.status();
    let cookies = resp
        .headers()
        .get_all(reqwest::header::SET_COOKIE)
        .iter()
        .filter_map(|v| v.to_str().ok().map(String::from))
        .collect();
    let body = resp.text().map_err(|e| e.to_string())?;
    Ok(HttpOutput {
        status: status.as_u16(),
        ok: status.is_success(),
        body,
        set_cookies: cookies,
    })
}

#[tauri::command]
fn window_start_drag(window: Window) -> Result<(), String> {
    window.start_dragging().map_err(|e| e.to_string())
}

#[tauri::command]
fn window_minimize(window: Window) -> Result<(), String> {
    window.minimize().map_err(|e| e.to_string())
}

#[tauri::command]
fn window_toggle_maximize(window: Window) -> Result<(), String> {
    let max = window.is_maximized().map_err(|e| e.to_string())?;
    if max {
        window.unmaximize().map_err(|e| e.to_string())
    } else {
        window.maximize().map_err(|e| e.to_string())
    }
}

#[tauri::command]
fn window_request_close(window: Window) -> Result<(), String> {
    hide_to_tray(&window);
    Ok(())
}

#[tauri::command]
fn app_exit(app: AppHandle) -> Result<(), String> {
    save_bounds_on_exit(&app);
    app.exit(0);
    Ok(())
}

#[tauri::command]
fn open_additional_window(app: AppHandle) -> Result<(), String> {
    create_additional_window(&app)
}

#[tauri::command]
fn set_auto_start(enable: bool) -> Result<(), String> {
    let exe_path = std::env::current_exe()
        .map_err(|e| e.to_string())?
        .to_string_lossy()
        .to_string();
    let key = r"SOFTWARE\Microsoft\Windows\CurrentVersion\Run";
    let value_name = "CertKeeperDesktop";

    #[cfg(target_os = "windows")]
    {
        use windows_sys::Win32::System::Registry::*;
        let mut h_key: HKEY = core::ptr::null_mut();
        let result = unsafe {
            RegOpenKeyExW(HKEY_CURRENT_USER, encode_wide(key).as_ptr(), 0, KEY_SET_VALUE, &mut h_key)
        };
        if result != 0 {
            return Err(format!("RegOpenKeyEx failed: {}", result));
        }
        unsafe { RegCloseKey(h_key) };

        if enable {
            let mut h_key: HKEY = core::ptr::null_mut();
            let result = unsafe {
                RegOpenKeyExW(HKEY_CURRENT_USER, encode_wide(key).as_ptr(), 0, KEY_SET_VALUE, &mut h_key)
            };
            if result != 0 {
                return Err(format!("RegOpenKeyEx failed: {}", result));
            }
            let data = encode_wide(&format!("\"{}\"", exe_path));
            let result = unsafe {
                RegSetValueExW(
                    h_key,
                    encode_wide(value_name).as_ptr(),
                    0,
                    REG_SZ as u32,
                    data.as_ptr() as *const u8,
                    (data.len() * 2) as u32,
                )
            };
            unsafe { RegCloseKey(h_key) };
            if result != 0 {
                return Err(format!("RegSetValueEx failed: {}", result));
            }
        } else {
            let mut h_key: HKEY = core::ptr::null_mut();
            let result = unsafe {
                RegOpenKeyExW(HKEY_CURRENT_USER, encode_wide(key).as_ptr(), 0, KEY_SET_VALUE, &mut h_key)
            };
            if result != 0 {
                return Err(format!("RegOpenKeyEx failed: {}", result));
            }
            let result = unsafe { RegDeleteValueW(h_key, encode_wide(value_name).as_ptr()) };
            unsafe { RegCloseKey(h_key) };
            if result != 0 && result != 2 {
                return Err(format!("RegDeleteValue failed: {}", result));
            }
        }
    }

    Ok(())
}

fn encode_wide(s: &str) -> Vec<u16> {
    s.encode_utf16().chain(std::iter::once(0u16)).collect()
}

#[tauri::command]
fn config_dir(app: AppHandle) -> Result<String, String> {
    app.path()
        .app_config_dir()
        .map(|p| p.display().to_string())
        .map_err(|e| e.to_string())
}

#[tauri::command]
fn save_app_config(app: AppHandle, config: AppConfig) -> Result<(), String> {
    let path = config_path(&app)?;
    let content = serde_json::to_string_pretty(&config).map_err(|e| e.to_string())?;
    fs::write(&path, content).map_err(|e| e.to_string())
}

#[tauri::command]
fn load_app_config(app: AppHandle) -> Result<AppConfig, String> {
    let path = config_path(&app)?;
    if !path.exists() {
        return Ok(AppConfig::default());
    }
    let content = fs::read_to_string(&path).map_err(|e| e.to_string())?;
    serde_json::from_str(&content).map_err(|e| e.to_string())
}

#[tauri::command]
fn save_window_bounds(app: AppHandle, bounds: WindowBounds) -> Result<(), String> {
    let path = bounds_path(&app)?;
    let content = serde_json::to_string_pretty(&bounds).map_err(|e| e.to_string())?;
    fs::write(&path, content).map_err(|e| e.to_string())
}

#[tauri::command]
fn load_window_bounds(app: AppHandle) -> Result<WindowBounds, String> {
    let path = bounds_path(&app)?;
    if !path.exists() {
        return Ok(WindowBounds::default());
    }
    let content = fs::read_to_string(&path).map_err(|e| e.to_string())?;
    serde_json::from_str(&content).map_err(|e| e.to_string())
}

#[tauri::command]
fn read_login_profiles(app: AppHandle) -> Result<LoginProfilesFile, String> {
    read_login_profiles_file(&app)
}

#[tauri::command]
fn save_login_profile(
    app: AppHandle,
    email: String,
    password: String,
    auto_login: bool,
) -> Result<(), String> {
    let mut data = read_login_profiles_file(&app)?;

    #[cfg(target_os = "windows")]
    let encrypted = STANDARD.encode(dpapi_encrypt(password.as_bytes())?);
    #[cfg(not(target_os = "windows"))]
    let encrypted = STANDARD.encode(password.as_bytes());

    if let Some(existing) = data.profiles.iter_mut().find(|p| p.email == email) {
        existing.encrypted_password = encrypted;
        existing.auto_login = auto_login;
    } else {
        data.profiles.push(LoginProfile {
            email: email.clone(),
            encrypted_password: encrypted,
            auto_login,
        });
    }
    data.last_used_email = Some(email);
    write_login_profiles_file(&app, &data)
}

#[tauri::command]
fn delete_login_profile(app: AppHandle, email: String) -> Result<(), String> {
    let mut data = read_login_profiles_file(&app)?;
    data.profiles.retain(|p| p.email != email);
    if data.last_used_email.as_ref() == Some(&email) {
        data.last_used_email = data.profiles.first().map(|p| p.email.clone());
    }
    write_login_profiles_file(&app, &data)
}

#[tauri::command]
fn decrypt_login_password(app: AppHandle, email: String) -> Result<String, String> {
    let data = read_login_profiles_file(&app)?;
    let profile = data
        .profiles
        .iter()
        .find(|p| p.email == email)
        .ok_or("profile not found")?;
    let bytes = STANDARD
        .decode(&profile.encrypted_password)
        .map_err(|err| err.to_string())?;

    #[cfg(target_os = "windows")]
    let decrypted = dpapi_decrypt(&bytes)?;
    #[cfg(not(target_os = "windows"))]
    let decrypted = bytes;

    String::from_utf8(decrypted).map_err(|err| err.to_string())
}

// ─── Helpers ───

fn config_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|e| e.to_string())?
        .join("app-config.json"))
}

fn bounds_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|e| e.to_string())?
        .join("window-state.json"))
}

fn login_profiles_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|e| e.to_string())?
        .join("login-profiles.json"))
}

fn ensure_config_dir(app: &AppHandle) -> Result<(), String> {
    let dir = app.path().app_config_dir().map_err(|e| e.to_string())?;
    fs::create_dir_all(dir).map_err(|e| e.to_string())
}

fn read_login_profiles_file(app: &AppHandle) -> Result<LoginProfilesFile, String> {
    let path = login_profiles_path(app)?;
    if !path.exists() {
        return Ok(LoginProfilesFile::default());
    }
    let content = fs::read_to_string(&path).map_err(|e| e.to_string())?;
    serde_json::from_str(&content).map_err(|e| e.to_string())
}

fn write_login_profiles_file(app: &AppHandle, data: &LoginProfilesFile) -> Result<(), String> {
    let path = login_profiles_path(app)?;
    let content = serde_json::to_string_pretty(data).map_err(|e| e.to_string())?;
    fs::write(&path, content).map_err(|e| e.to_string())
}

fn hide_to_tray(window: &Window) {
    if let Ok(pos) = window.outer_position() {
        let scale = window.scale_factor().unwrap_or(1.0);
        if let Ok(size) = window.inner_size() {
            let max = window.is_maximized().unwrap_or(false);
            let bounds = WindowBounds {
                x: pos.x,
                y: pos.y,
                width: (size.width as f64 / scale) as u32,
                height: (size.height as f64 / scale) as u32,
                maximized: max,
            };
            let app = window.app_handle();
            let _ = save_window_bounds(app.clone(), bounds);
        }
    }
    let _ = window.hide();
}

fn save_bounds_on_exit(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        if let Ok(pos) = window.outer_position() {
            let scale = window.scale_factor().unwrap_or(1.0);
            if let Ok(size) = window.inner_size() {
                let max = window.is_maximized().unwrap_or(false);
                let bounds = WindowBounds {
                    x: pos.x,
                    y: pos.y,
                    width: (size.width as f64 / scale) as u32,
                    height: (size.height as f64 / scale) as u32,
                    maximized: max,
                };
                let _ = save_window_bounds(app.clone(), bounds);
            }
        }
    }
}

fn current_close_action(app: &AppHandle) -> String {
    load_app_config(app.clone())
        .ok()
        .and_then(|config| config.close_action)
        .map(|value| value.trim().to_lowercase())
        .filter(|value| value == "ask" || value == "tray" || value == "exit")
        .unwrap_or_else(|| "ask".to_string())
}

fn request_frontend_close_confirmation(window: &Window) {
    let _ = window.emit("app-close-requested", ());
}

fn request_second_launch_confirmation(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.emit("second-launch-requested", ());
        ensure_window_visible(&window);
    }
}

fn installer_requested_quit(args: &[String]) -> bool {
    args.iter().any(|arg| arg == INSTALLER_QUIT_ARG)
}

fn handle_installer_quit_request(app: &AppHandle) {
    save_bounds_on_exit(app);
    app.exit(0);
}

fn ensure_window_visible(window: &WebviewWindow) {
    let _ = window.show();
    let _ = window.unminimize();
    let _ = window.set_focus();
}

fn apply_window_bounds(window: &WebviewWindow, bounds: &WindowBounds) {
    if bounds.width > 0 {
        let _ = window.set_size(tauri::LogicalSize::new(
            bounds.width as f64,
            bounds.height as f64,
        ));
        let _ = window.set_position(tauri::Position::Logical(tauri::LogicalPosition::new(
            bounds.x as f64,
            bounds.y as f64,
        )));
        if bounds.maximized {
            let _ = window.maximize();
        }
    }
}

fn set_window_icon(window: &WebviewWindow) {
    if let Ok(window_icon) = Image::from_bytes(include_bytes!("../icons/icon-256.png")) {
        let _ = window.set_icon(window_icon);
    }
}

fn create_additional_window(app: &AppHandle) -> Result<(), String> {
    let index = app.webview_windows().len();
    let label = format!(
        "client-{}",
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map_err(|e| e.to_string())?
            .as_millis()
    );
    let window = WebviewWindowBuilder::new(app, label, WebviewUrl::App("index.html".into()))
        .title(&format!("证书管家 - 客户端 {}", index))
        .inner_size(1100.0, 750.0)
        .min_inner_size(900.0, 600.0)
        .resizable(true)
        .decorations(false)
        .visible(false)
        .build()
        .map_err(|e| e.to_string())?;
    if let Ok(bounds) = load_window_bounds(app.clone()) {
        apply_window_bounds(&window, &bounds);
    }
    set_window_icon(&window);
    ensure_window_visible(&window);
    Ok(())
}

fn setup_tray(app: &AppHandle) -> tauri::Result<()> {
    let show = MenuItem::with_id(app, "show", "打开主窗口", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&show, &quit])?;

    let tray_icon = Image::from_bytes(include_bytes!("../icons/icon-256.png"))?;
    TrayIconBuilder::new()
        .icon(tray_icon)
        .tooltip("证书管家")
        .menu(&menu)
        .on_menu_event(move |app, event| match event.id.as_ref() {
            "show" => {
                if let Some(w) = app.get_webview_window("main") {
                    ensure_window_visible(&w);
                }
            }
            "quit" => {
                app.exit(0);
            }
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                if let Some(w) = tray.app_handle().get_webview_window("main") {
                    ensure_window_visible(&w);
                }
            }
        })
        .build(app)?;
    Ok(())
}

fn main() {
    let http_state = AppHttpState::new().expect("failed to create HTTP client");
    let installer_quit_on_launch = installer_requested_quit(&std::env::args().skip(1).collect::<Vec<_>>());
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, args, _| {
            if installer_requested_quit(&args) {
                handle_installer_quit_request(app);
                return;
            }
            request_second_launch_confirmation(app);
        }))
        .manage(http_state)
        .manage(WindowState::default())
        .setup(move |app| {
            ensure_config_dir(app.handle())
                .map_err(|e| -> Box<dyn std::error::Error> { e.into() })?;
            if installer_quit_on_launch {
                handle_installer_quit_request(app.handle());
                return Ok(());
            }
            setup_tray(app.handle())?;
            if let Some(window) = app.get_webview_window("main") {
                if let Ok(bounds) = load_window_bounds(app.handle().clone()) {
                    apply_window_bounds(&window, &bounds);
                }
                let _ = window.set_title("证书管家");
                set_window_icon(&window);
                let _ = window.show();
            }
            Ok(())
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                match current_close_action(&window.app_handle()).as_str() {
                    "exit" => {
                        save_bounds_on_exit(&window.app_handle());
                        window.app_handle().exit(0);
                    }
                    "tray" => hide_to_tray(window),
                    _ => request_frontend_close_confirmation(window),
                }
            }
        })
        .invoke_handler(tauri::generate_handler![
            http_request,
            window_start_drag,
            window_minimize,
            window_toggle_maximize,
            window_request_close,
            app_exit,
            open_additional_window,
            set_auto_start,
            config_dir,
            save_app_config,
            load_app_config,
            save_window_bounds,
            load_window_bounds,
            read_login_profiles,
            save_login_profile,
            delete_login_profile,
            decrypt_login_password,
        ])
        .build(tauri::generate_context!())
        .expect("failed to build CertKeeper");

    let _ = app.run(|_, _| {});
}
