#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::collections::HashMap;
use std::ffi::OsStr;
use std::fs::{self, OpenOptions};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

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

#[cfg(target_os = "windows")]
use std::os::windows::process::CommandExt;

const INSTALLER_QUIT_ARG: &str = "--quit-for-install";
const CREATE_NO_WINDOW: u32 = 0x08000000;
const P2P_RUNTIME_RELATIVE_PATH: &str = "runtime/easytier-core.exe";
const P2P_CLI_RELATIVE_PATH: &str = "runtime/easytier-cli.exe";
const USER_P2P_TCP_LISTENER: &str = "tcp://0.0.0.0:21010";
const USER_P2P_UDP_LISTENER: &str = "udp://0.0.0.0:21010";
const USER_P2P_RPC_PORTAL: &str = "127.0.0.1:29888";

fn background_command(program: impl AsRef<OsStr>) -> Command {
    let mut command = Command::new(program);
    #[cfg(target_os = "windows")]
    command.creation_flags(CREATE_NO_WINDOW);
    command
}

struct AppHttpState {
    client: Client,
}

impl AppHttpState {
    fn new() -> Result<Self, String> {
        let client = Client::builder()
            .redirect(reqwest::redirect::Policy::limited(10))
            .timeout(Duration::from_secs(120))
            .build()
            .map_err(|err| err.to_string())?;
        Ok(Self { client })
    }
}

#[derive(Default)]
struct P2PRuntimeManagerState {
    child: Mutex<Option<Child>>,
    launch: Mutex<P2PRuntimeLaunchState>,
    op: Mutex<()>,
}

#[derive(Clone, Default)]
struct P2PRuntimeLaunchState {
    available: bool,
    configured: bool,
    pid: Option<u32>,
    started_at: Option<u64>,
    executable_path: String,
    work_dir: String,
    stdout_log_path: String,
    stderr_log_path: String,
    args_summary: String,
    last_error: String,
    machine_id: String,
    rpc_portal: String,
    node_hostname: String,
    virtual_ipv4: String,
    instance_id: String,
    peer_count: usize,
    connected_peers: Vec<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct P2PRuntimeStatus {
    available: bool,
    configured: bool,
    running: bool,
    pid: Option<u32>,
    started_at: Option<u64>,
    executable_path: String,
    work_dir: String,
    stdout_log_path: String,
    stderr_log_path: String,
    args_summary: String,
    last_error: String,
    machine_id: String,
    rpc_portal: String,
    node_hostname: String,
    virtual_ipv4: String,
    instance_id: String,
    peer_count: usize,
    connected_peers: Vec<String>,
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
    #[serde(rename = "p2pAutoStart")]
    p2p_auto_start: Option<bool>,
    #[serde(rename = "driveFallbackPolicy")]
    drive_fallback_policy: Option<String>,
    #[serde(rename = "imageBulkUploadMode")]
    image_bulk_upload_mode: Option<String>,
    #[serde(rename = "driveCloudUrl")]
    drive_cloud_url: Option<String>,
    #[serde(rename = "driveP2pUrl")]
    drive_p2p_url: Option<String>,
    #[serde(rename = "galleryCloudUrl")]
    gallery_cloud_url: Option<String>,
    #[serde(rename = "galleryP2pUrl")]
    gallery_p2p_url: Option<String>,
    #[serde(rename = "p2pNetworkName")]
    p2p_network_name: Option<String>,
    #[serde(rename = "p2pNetworkSecret")]
    p2p_network_secret: Option<String>,
    #[serde(rename = "p2pPeerUrl")]
    p2p_peer_url: Option<String>,
    #[serde(rename = "p2pVirtualIpv4")]
    p2p_virtual_ipv4: Option<String>,
    #[serde(rename = "p2pUseDhcp")]
    p2p_use_dhcp: Option<bool>,
    #[serde(rename = "p2pInstanceName")]
    p2p_instance_name: Option<String>,
    #[serde(rename = "p2pHostname")]
    p2p_hostname: Option<String>,
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

#[tauri::command]
fn http_request(state: State<'_, AppHttpState>, input: HttpInput) -> Result<HttpOutput, String> {
    let method = input
        .method
        .as_deref()
        .unwrap_or("GET")
        .parse::<Method>()
        .map_err(|err| err.to_string())?;
    let mut req = state.client.request(method, &input.url);
    if let Some(headers) = input.headers {
        let mut hm = HeaderMap::new();
        for (key, value) in headers {
            let name = HeaderName::from_bytes(key.as_bytes()).map_err(|err| err.to_string())?;
            let value = HeaderValue::from_str(&value).map_err(|err| err.to_string())?;
            hm.insert(name, value);
        }
        req = req.headers(hm);
    }
    if let Some(body) = input.body {
        req = req.body(body);
    }
    let resp = req.send().map_err(|err| err.to_string())?;
    let status = resp.status();
    let cookies = resp
        .headers()
        .get_all(reqwest::header::SET_COOKIE)
        .iter()
        .filter_map(|value| value.to_str().ok().map(str::to_owned))
        .collect();
    let body = resp.text().map_err(|err| err.to_string())?;
    Ok(HttpOutput {
        status: status.as_u16(),
        ok: status.is_success(),
        body,
        set_cookies: cookies,
    })
}

#[tauri::command]
fn p2p_runtime_status(
    app: AppHandle,
    runtime: State<'_, P2PRuntimeManagerState>,
) -> Result<P2PRuntimeStatus, String> {
    current_p2p_runtime_status(&app, &runtime)
}

#[tauri::command]
fn p2p_runtime_start(
    app: AppHandle,
    runtime: State<'_, P2PRuntimeManagerState>,
) -> Result<P2PRuntimeStatus, String> {
    start_p2p_runtime_internal(&app, &runtime)
}

#[tauri::command]
fn p2p_runtime_stop(
    app: AppHandle,
    runtime: State<'_, P2PRuntimeManagerState>,
) -> Result<P2PRuntimeStatus, String> {
    stop_p2p_runtime_process(&app, &runtime)?;
    current_p2p_runtime_status(&app, &runtime)
}

#[tauri::command]
fn open_p2p_runtime_log(
    app: AppHandle,
    runtime: State<'_, P2PRuntimeManagerState>,
    kind: String,
) -> Result<(), String> {
    let launch = runtime
        .launch
        .lock()
        .map_err(|_| "P2P runtime 锁不可用".to_string())?
        .clone();
    let path = match kind.as_str() {
        "stdout" => {
            if launch.stdout_log_path.trim().is_empty() {
                p2p_runtime_stdout_log_path(&app)?
            } else {
                PathBuf::from(launch.stdout_log_path)
            }
        }
        "stderr" => {
            if launch.stderr_log_path.trim().is_empty() {
                p2p_runtime_stderr_log_path(&app)?
            } else {
                PathBuf::from(launch.stderr_log_path)
            }
        }
        _ => return Err("未知日志类型".to_string()),
    };
    open_path(path);
    Ok(())
}

#[tauri::command]
fn window_start_drag(window: Window) -> Result<(), String> {
    window.start_dragging().map_err(|err| err.to_string())
}

#[tauri::command]
fn window_minimize(window: Window) -> Result<(), String> {
    window.minimize().map_err(|err| err.to_string())
}

#[tauri::command]
fn window_toggle_maximize(window: Window) -> Result<(), String> {
    let maximized = window.is_maximized().map_err(|err| err.to_string())?;
    if maximized {
        window.unmaximize().map_err(|err| err.to_string())
    } else {
        window.maximize().map_err(|err| err.to_string())
    }
}

#[tauri::command]
fn window_request_close(window: Window) -> Result<(), String> {
    hide_to_tray(&window);
    Ok(())
}

#[tauri::command]
fn app_exit(app: AppHandle) -> Result<(), String> {
    if let Some(runtime) = app.try_state::<P2PRuntimeManagerState>() {
        let _ = stop_p2p_runtime_process(&app, &runtime);
    }
    save_bounds_on_exit(&app);
    app.exit(0);
    Ok(())
}

#[tauri::command]
fn open_external(url: String) -> Result<(), String> {
    #[cfg(target_os = "windows")]
    {
        std::process::Command::new("cmd")
            .args(["/C", "start", "", &url])
            .spawn()
            .map_err(|err| err.to_string())?;
        return Ok(());
    }

    #[cfg(target_os = "linux")]
    {
        std::process::Command::new("xdg-open")
            .arg(&url)
            .spawn()
            .map_err(|err| err.to_string())?;
        return Ok(());
    }

    #[cfg(target_os = "macos")]
    {
        std::process::Command::new("open")
            .arg(&url)
            .spawn()
            .map_err(|err| err.to_string())?;
        return Ok(());
    }

    #[allow(unreachable_code)]
    Err("unsupported platform".to_string())
}

#[tauri::command]
fn open_additional_window(app: AppHandle) -> Result<(), String> {
    create_additional_window(&app)
}

#[tauri::command]
fn set_auto_start(enable: bool) -> Result<(), String> {
    let exe_path = std::env::current_exe()
        .map_err(|err| err.to_string())?
        .to_string_lossy()
        .to_string();
    let key = r"SOFTWARE\Microsoft\Windows\CurrentVersion\Run";
    let value_name = "CloudRelayUserDesktop";

    #[cfg(target_os = "windows")]
    {
        use windows_sys::Win32::System::Registry::*;
        let mut h_key: HKEY = core::ptr::null_mut();
        let result = unsafe {
            RegOpenKeyExW(
                HKEY_CURRENT_USER,
                encode_wide(key).as_ptr(),
                0,
                KEY_SET_VALUE,
                &mut h_key,
            )
        };
        if result != 0 {
            return Err(format!("RegOpenKeyEx failed: {}", result));
        }
        unsafe { RegCloseKey(h_key) };

        if enable {
            let mut h_key: HKEY = core::ptr::null_mut();
            let result = unsafe {
                RegOpenKeyExW(
                    HKEY_CURRENT_USER,
                    encode_wide(key).as_ptr(),
                    0,
                    KEY_SET_VALUE,
                    &mut h_key,
                )
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
                RegOpenKeyExW(
                    HKEY_CURRENT_USER,
                    encode_wide(key).as_ptr(),
                    0,
                    KEY_SET_VALUE,
                    &mut h_key,
                )
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

#[tauri::command]
fn config_dir(app: AppHandle) -> Result<String, String> {
    app.path()
        .app_config_dir()
        .map(|path| path.display().to_string())
        .map_err(|err| err.to_string())
}

#[tauri::command]
fn log_dir(app: AppHandle) -> Result<String, String> {
    app.path()
        .app_log_dir()
        .map(|path| path.display().to_string())
        .map_err(|err| err.to_string())
}

#[tauri::command]
fn save_app_config(app: AppHandle, config: AppConfig) -> Result<(), String> {
    let path = config_path(&app)?;
    let content = serde_json::to_string_pretty(&config).map_err(|err| err.to_string())?;
    fs::write(&path, content).map_err(|err| err.to_string())
}

#[tauri::command]
fn load_app_config(app: AppHandle) -> Result<AppConfig, String> {
    let path = config_path(&app)?;
    if !path.exists() {
        return Ok(AppConfig::default());
    }
    let content = fs::read_to_string(&path).map_err(|err| err.to_string())?;
    serde_json::from_str(&content).map_err(|err| err.to_string())
}

#[tauri::command]
fn save_window_bounds(app: AppHandle, bounds: WindowBounds) -> Result<(), String> {
    let path = bounds_path(&app)?;
    let content = serde_json::to_string_pretty(&bounds).map_err(|err| err.to_string())?;
    fs::write(&path, content).map_err(|err| err.to_string())
}

#[tauri::command]
fn load_window_bounds(app: AppHandle) -> Result<WindowBounds, String> {
    let path = bounds_path(&app)?;
    if !path.exists() {
        return Ok(WindowBounds::default());
    }
    let content = fs::read_to_string(&path).map_err(|err| err.to_string())?;
    serde_json::from_str(&content).map_err(|err| err.to_string())
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

    if let Some(existing) = data
        .profiles
        .iter_mut()
        .find(|profile| profile.email == email)
    {
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
    data.profiles.retain(|profile| profile.email != email);
    if data.last_used_email.as_ref() == Some(&email) {
        data.last_used_email = data.profiles.first().map(|profile| profile.email.clone());
    }
    write_login_profiles_file(&app, &data)
}

#[tauri::command]
fn decrypt_login_password(app: AppHandle, email: String) -> Result<String, String> {
    let data = read_login_profiles_file(&app)?;
    let profile = data
        .profiles
        .iter()
        .find(|profile| profile.email == email)
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

fn encode_wide(value: &str) -> Vec<u16> {
    value.encode_utf16().chain(std::iter::once(0u16)).collect()
}

fn config_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|err| err.to_string())?
        .join("app-config.json"))
}

fn bounds_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|err| err.to_string())?
        .join("window-state.json"))
}

fn login_profiles_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|err| err.to_string())?
        .join("login-profiles.json"))
}

fn ensure_config_dir(app: &AppHandle) -> Result<(), String> {
    let dir = app.path().app_config_dir().map_err(|err| err.to_string())?;
    fs::create_dir_all(dir).map_err(|err| err.to_string())
}

fn read_login_profiles_file(app: &AppHandle) -> Result<LoginProfilesFile, String> {
    let path = login_profiles_path(app)?;
    if !path.exists() {
        return Ok(LoginProfilesFile::default());
    }
    let content = fs::read_to_string(&path).map_err(|err| err.to_string())?;
    serde_json::from_str(&content).map_err(|err| err.to_string())
}

fn write_login_profiles_file(app: &AppHandle, data: &LoginProfilesFile) -> Result<(), String> {
    let path = login_profiles_path(app)?;
    let content = serde_json::to_string_pretty(data).map_err(|err| err.to_string())?;
    fs::write(&path, content).map_err(|err| err.to_string())
}

fn p2p_runtime_work_dir(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|err| err.to_string())?
        .join("p2p-runtime"))
}

fn p2p_runtime_stdout_log_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_log_dir()
        .map_err(|err| err.to_string())?
        .join("p2p-runtime-stdout.log"))
}

fn p2p_runtime_stderr_log_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_log_dir()
        .map_err(|err| err.to_string())?
        .join("p2p-runtime-stderr.log"))
}

fn p2p_runtime_pid_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(p2p_runtime_work_dir(app)?.join("easytier.pid"))
}

fn p2p_runtime_machine_id_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(p2p_runtime_work_dir(app)?.join("machine-id.txt"))
}

fn ensure_p2p_runtime_dirs(app: &AppHandle) -> Result<(), String> {
    let config_dir = app.path().app_config_dir().map_err(|err| err.to_string())?;
    let log_dir = app.path().app_log_dir().map_err(|err| err.to_string())?;
    let runtime_dir = p2p_runtime_work_dir(app)?;
    for dir in [config_dir, log_dir, runtime_dir] {
        fs::create_dir_all(dir).map_err(|err| err.to_string())?;
    }
    Ok(())
}

fn resolve_p2p_runtime_executable(app: &AppHandle) -> Result<PathBuf, String> {
    let exe_dir = std::env::current_exe()
        .ok()
        .and_then(|path| path.parent().map(|dir| dir.to_path_buf()));

    let mut candidates: Vec<PathBuf> = Vec::new();
    if let Some(ref dir) = exe_dir {
        candidates.push(dir.join("easytier-core.exe"));
        candidates.push(dir.join("runtime").join("easytier-core.exe"));
    }
    if let Ok(resource_dir) = app.path().resource_dir() {
        candidates.push(resource_dir.join(P2P_RUNTIME_RELATIVE_PATH));
    }
    candidates.push(
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../../../deploy/windows/user-console/runtime/easytier-core.exe"),
    );

    for candidate in &candidates {
        if candidate.exists() {
            return Ok(candidate.clone());
        }
    }
    Err(format!(
        "未找到 bundled easytier-core.exe (搜索了 {} 个路径)",
        candidates.len()
    ))
}

fn resolve_p2p_cli_executable(app: &AppHandle) -> Result<PathBuf, String> {
    let exe_dir = std::env::current_exe()
        .ok()
        .and_then(|path| path.parent().map(|dir| dir.to_path_buf()));

    let mut candidates: Vec<PathBuf> = Vec::new();
    if let Some(ref dir) = exe_dir {
        candidates.push(dir.join("easytier-cli.exe"));
        candidates.push(dir.join("runtime").join("easytier-cli.exe"));
    }
    if let Ok(resource_dir) = app.path().resource_dir() {
        candidates.push(resource_dir.join(P2P_CLI_RELATIVE_PATH));
    }
    candidates.push(
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../../../deploy/windows/user-console/runtime/easytier-cli.exe"),
    );

    for candidate in &candidates {
        if candidate.exists() {
            return Ok(candidate.clone());
        }
    }
    Err(format!(
        "未找到 bundled easytier-cli.exe (搜索了 {} 个路径)",
        candidates.len()
    ))
}

fn trim_option(value: Option<&str>) -> Option<String> {
    value
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_string)
}

fn require_trimmed(value: Option<&str>, message: &str) -> Result<String, String> {
    trim_option(value).ok_or_else(|| message.to_string())
}

fn split_p2p_peers(value: Option<&str>) -> Vec<String> {
    trim_option(value)
        .map(|value| {
            value
                .split(|ch: char| {
                    ch == '\n' || ch == '\r' || ch == ',' || ch == ';' || ch.is_whitespace()
                })
                .map(str::trim)
                .filter(|entry| !entry.is_empty())
                .map(str::to_string)
                .collect::<Vec<String>>()
        })
        .unwrap_or_default()
}

fn fnv1a64(bytes: &[u8]) -> u64 {
    let mut hash: u64 = 0xcbf29ce484222325;
    for byte in bytes {
        hash ^= u64::from(*byte);
        hash = hash.wrapping_mul(0x100000001b3);
    }
    hash
}

fn load_or_create_p2p_machine_id(app: &AppHandle) -> Result<String, String> {
    let path = p2p_runtime_machine_id_path(app)?;
    if path.exists() {
        let value = fs::read_to_string(&path).map_err(|err| err.to_string())?;
        let value = value.trim();
        if !value.is_empty() {
            return Ok(value.to_string());
        }
    }
    let config_dir = app.path().app_config_dir().map_err(|err| err.to_string())?;
    let seed = format!(
        "{}:{}:{}",
        config_dir.display(),
        now_millis(),
        std::process::id()
    );
    let machine_id = format!("cloud-relay-user-{:016x}", fnv1a64(seed.as_bytes()));
    fs::write(&path, &machine_id).map_err(|err| err.to_string())?;
    Ok(machine_id)
}

fn read_p2p_runtime_pid(app: &AppHandle) -> Result<Option<u32>, String> {
    let path = p2p_runtime_pid_path(app)?;
    if !path.exists() {
        return Ok(None);
    }
    let content = fs::read_to_string(&path).map_err(|err| err.to_string())?;
    let pid = content.trim().parse::<u32>().ok();
    Ok(pid)
}

fn write_p2p_runtime_pid(app: &AppHandle, pid: u32) -> Result<(), String> {
    let path = p2p_runtime_pid_path(app)?;
    fs::write(path, pid.to_string()).map_err(|err| err.to_string())
}

fn clear_p2p_runtime_pid(app: &AppHandle) -> Result<(), String> {
    let path = p2p_runtime_pid_path(app)?;
    if path.exists() {
        fs::remove_file(path).map_err(|err| err.to_string())?;
    }
    Ok(())
}

fn is_process_running(pid: u32) -> bool {
    #[cfg(target_os = "windows")]
    {
        let output = background_command("tasklist")
            .args(["/FI", &format!("PID eq {pid}")])
            .output();
        return output
            .ok()
            .filter(|result| result.status.success())
            .map(|result| String::from_utf8_lossy(&result.stdout).contains(&pid.to_string()))
            .unwrap_or(false);
    }
    #[cfg(not(target_os = "windows"))]
    {
        Command::new("kill")
            .args(["-0", &pid.to_string()])
            .status()
            .map(|status| status.success())
            .unwrap_or(false)
    }
}

fn kill_process_by_pid(pid: u32) -> Result<(), String> {
    #[cfg(target_os = "windows")]
    {
        let status = background_command("taskkill")
            .args(["/PID", &pid.to_string(), "/T", "/F"])
            .status()
            .map_err(|err| format!("停止 EasyTier 进程失败: {err}"))?;
        if !status.success() {
            return Err(format!(
                "停止 EasyTier 进程失败: taskkill exited with {status}"
            ));
        }
        return Ok(());
    }
    #[cfg(not(target_os = "windows"))]
    {
        let status = Command::new("kill")
            .args(["-TERM", &pid.to_string()])
            .status()
            .map_err(|err| format!("停止 EasyTier 进程失败: {err}"))?;
        if !status.success() {
            return Err(format!("停止 EasyTier 进程失败: kill exited with {status}"));
        }
        Ok(())
    }
}

fn query_p2p_runtime_json(
    app: &AppHandle,
    subcommand: &[&str],
) -> Result<serde_json::Value, String> {
    let cli = resolve_p2p_cli_executable(app)?;
    let output = background_command(cli)
        .args(["-p", USER_P2P_RPC_PORTAL, "-o", "json"])
        .args(subcommand)
        .output()
        .map_err(|err| format!("调用 easytier-cli 失败: {err}"))?;
    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr).trim().to_string();
        let stdout = String::from_utf8_lossy(&output.stdout).trim().to_string();
        let detail = if !stderr.is_empty() { stderr } else { stdout };
        return Err(format!("读取 EasyTier 运行态失败: {detail}"));
    }
    serde_json::from_slice(&output.stdout).map_err(|err| err.to_string())
}

fn refresh_p2p_runtime_live_snapshot(
    app: &AppHandle,
    launch: &mut P2PRuntimeLaunchState,
    running: bool,
) {
    launch.machine_id = load_or_create_p2p_machine_id(app).unwrap_or_default();
    launch.rpc_portal = USER_P2P_RPC_PORTAL.to_string();
    launch.node_hostname.clear();
    launch.virtual_ipv4.clear();
    launch.instance_id.clear();
    launch.peer_count = 0;
    launch.connected_peers.clear();
    if !running {
        return;
    }

    if let Ok(node) = query_p2p_runtime_json(app, &["node", "info"]) {
        launch.node_hostname = node
            .get("hostname")
            .and_then(|value| value.as_str())
            .unwrap_or_default()
            .to_string();
        launch.virtual_ipv4 = node
            .get("ipv4_addr")
            .and_then(|value| value.as_str())
            .unwrap_or_default()
            .to_string();
        launch.instance_id = node
            .get("inst_id")
            .and_then(|value| value.as_str())
            .unwrap_or_default()
            .to_string();
    }

    if let Ok(peers) = query_p2p_runtime_json(app, &["peer", "list"]) {
        if let Some(items) = peers.as_array() {
            let peers = items
                .iter()
                .filter(|item| item.get("cost").and_then(|value| value.as_str()) != Some("Local"))
                .map(|item| {
                    let hostname = item
                        .get("hostname")
                        .and_then(|value| value.as_str())
                        .unwrap_or("未知节点");
                    let ipv4 = item
                        .get("ipv4")
                        .and_then(|value| value.as_str())
                        .unwrap_or_default();
                    let tunnel = item
                        .get("tunnel_proto")
                        .and_then(|value| value.as_str())
                        .unwrap_or("-");
                    let latency = item
                        .get("lat_ms")
                        .and_then(|value| value.as_str())
                        .unwrap_or("-");
                    let ipv4_suffix = if ipv4.is_empty() {
                        String::new()
                    } else {
                        format!(" {ipv4}")
                    };
                    format!("{hostname}{ipv4_suffix} / {tunnel} / {latency} ms")
                })
                .collect::<Vec<String>>();
            launch.peer_count = peers.len();
            launch.connected_peers = peers;
        }
    }
}

fn build_p2p_runtime_args_with_app(
    app: &AppHandle,
    config: &AppConfig,
    file_log_dir: &Path,
) -> Result<Vec<String>, String> {
    let network_name = require_trimmed(
        config.p2p_network_name.as_deref(),
        "请先配置 EasyTier 网络名",
    )?;
    let network_secret = require_trimmed(
        config.p2p_network_secret.as_deref(),
        "请先配置 EasyTier 网络密钥",
    )?;
    let use_dhcp = config.p2p_use_dhcp.unwrap_or(true);
    let virtual_ipv4 = trim_option(config.p2p_virtual_ipv4.as_deref());
    if !use_dhcp && virtual_ipv4.is_none() {
        return Err("关闭 DHCP 后需要填写固定虚拟 IPv4".to_string());
    }

    let instance_name = trim_option(config.p2p_instance_name.as_deref())
        .unwrap_or_else(|| "cloud-relay-user".to_string());
    let hostname = trim_option(config.p2p_hostname.as_deref());
    let peers = split_p2p_peers(config.p2p_peer_url.as_deref());
    let machine_id = load_or_create_p2p_machine_id(app)?;

    let mut args = vec![
        "--network-name".to_string(),
        network_name,
        "--network-secret".to_string(),
        network_secret,
        "--machine-id".to_string(),
        machine_id,
        "-m".to_string(),
        instance_name,
        "--rpc-portal".to_string(),
        USER_P2P_RPC_PORTAL.to_string(),
        "--file-log-dir".to_string(),
        file_log_dir.display().to_string(),
        "--listeners".to_string(),
        USER_P2P_TCP_LISTENER.to_string(),
        "--listeners".to_string(),
        USER_P2P_UDP_LISTENER.to_string(),
    ];
    if let Some(hostname) = hostname {
        args.push("--hostname".to_string());
        args.push(hostname);
    }
    if use_dhcp {
        args.push("-d".to_string());
    } else if let Some(virtual_ipv4) = virtual_ipv4 {
        args.push("-i".to_string());
        args.push(virtual_ipv4);
    }
    for peer in peers {
        args.push("-p".to_string());
        args.push(peer);
    }
    Ok(args)
}

fn summarize_p2p_args(args: &[String]) -> String {
    let mut masked: Vec<String> = Vec::with_capacity(args.len());
    let mut hide_next = false;
    for arg in args {
        if hide_next {
            masked.push("******".to_string());
            hide_next = false;
            continue;
        }
        if arg == "--network-secret" {
            masked.push(arg.clone());
            hide_next = true;
            continue;
        }
        masked.push(arg.clone());
    }
    masked.join(" ")
}

fn current_p2p_runtime_status(
    app: &AppHandle,
    runtime: &P2PRuntimeManagerState,
) -> Result<P2PRuntimeStatus, String> {
    ensure_p2p_runtime_dirs(app)?;
    let log_dir = app.path().app_log_dir().map_err(|err| err.to_string())?;
    let executable_path = resolve_p2p_runtime_executable(app).ok();
    let config = load_app_config(app.clone()).unwrap_or_default();
    let args_preview = build_p2p_runtime_args_with_app(app, &config, &log_dir);

    let mut child_slot = runtime
        .child
        .lock()
        .map_err(|_| "P2P runtime 锁不可用".to_string())?;
    let mut launch = runtime
        .launch
        .lock()
        .map_err(|_| "P2P runtime 锁不可用".to_string())?;

    launch.available = executable_path.is_some();
    launch.configured = args_preview.is_ok();
    launch.work_dir = p2p_runtime_work_dir(app)?.display().to_string();
    launch.stdout_log_path = p2p_runtime_stdout_log_path(app)?.display().to_string();
    launch.stderr_log_path = p2p_runtime_stderr_log_path(app)?.display().to_string();
    if let Some(path) = executable_path {
        launch.executable_path = path.display().to_string();
    }
    if child_slot.is_none() {
        match args_preview {
            Ok(args) => launch.args_summary = summarize_p2p_args(&args),
            Err(_) => launch.args_summary.clear(),
        }
    }

    let running = if let Some(child) = child_slot.as_mut() {
        match child.try_wait() {
            Ok(Some(status)) => {
                launch.pid = None;
                launch.last_error = format!(
                    "EasyTier 进程已退出: {} / stderr: {}",
                    status, launch.stderr_log_path
                );
                *child_slot = None;
                let _ = clear_p2p_runtime_pid(app);
                false
            }
            Ok(None) => {
                launch.pid = Some(child.id());
                let _ = write_p2p_runtime_pid(app, child.id());
                true
            }
            Err(err) => {
                launch.last_error = format!("读取 EasyTier 状态失败: {err}");
                false
            }
        }
    } else {
        match read_p2p_runtime_pid(app)? {
            Some(pid) if is_process_running(pid) => {
                launch.pid = Some(pid);
                true
            }
            Some(_) => {
                let _ = clear_p2p_runtime_pid(app);
                launch.pid = None;
                false
            }
            None => false,
        }
    };

    refresh_p2p_runtime_live_snapshot(app, &mut launch, running);

    Ok(P2PRuntimeStatus {
        available: launch.available,
        configured: launch.configured,
        running,
        pid: launch.pid,
        started_at: launch.started_at,
        executable_path: launch.executable_path.clone(),
        work_dir: launch.work_dir.clone(),
        stdout_log_path: launch.stdout_log_path.clone(),
        stderr_log_path: launch.stderr_log_path.clone(),
        args_summary: launch.args_summary.clone(),
        last_error: launch.last_error.clone(),
        machine_id: launch.machine_id.clone(),
        rpc_portal: launch.rpc_portal.clone(),
        node_hostname: launch.node_hostname.clone(),
        virtual_ipv4: launch.virtual_ipv4.clone(),
        instance_id: launch.instance_id.clone(),
        peer_count: launch.peer_count,
        connected_peers: launch.connected_peers.clone(),
    })
}

fn stop_p2p_runtime_process(
    app: &AppHandle,
    runtime: &P2PRuntimeManagerState,
) -> Result<(), String> {
    let _op_guard = runtime
        .op
        .lock()
        .map_err(|_| "P2P runtime 锁不可用".to_string())?;
    let mut child_slot = runtime
        .child
        .lock()
        .map_err(|_| "P2P runtime 锁不可用".to_string())?;
    if let Some(mut child) = child_slot.take() {
        child
            .kill()
            .map_err(|err| format!("停止 EasyTier 失败: {err}"))?;
        let _ = child.wait();
    } else if let Some(pid) = read_p2p_runtime_pid(app)? {
        if is_process_running(pid) {
            kill_process_by_pid(pid)?;
        }
    }
    let _ = clear_p2p_runtime_pid(app);
    let mut launch = runtime
        .launch
        .lock()
        .map_err(|_| "P2P runtime 锁不可用".to_string())?;
    launch.pid = None;
    launch.peer_count = 0;
    launch.connected_peers.clear();
    Ok(())
}

fn start_p2p_runtime_internal(
    app: &AppHandle,
    runtime: &P2PRuntimeManagerState,
) -> Result<P2PRuntimeStatus, String> {
    let _op_guard = runtime
        .op
        .lock()
        .map_err(|_| "P2P runtime 锁不可用".to_string())?;
    ensure_p2p_runtime_dirs(app)?;
    let current = current_p2p_runtime_status(app, runtime)?;
    if current.running {
        return Ok(current);
    }
    let executable_path = resolve_p2p_runtime_executable(app)?;
    let work_dir = p2p_runtime_work_dir(app)?;
    let stdout_log_path = p2p_runtime_stdout_log_path(app)?;
    let stderr_log_path = p2p_runtime_stderr_log_path(app)?;
    let log_dir = app.path().app_log_dir().map_err(|err| err.to_string())?;
    let config = load_app_config(app.clone())?;
    let args = build_p2p_runtime_args_with_app(app, &config, &log_dir)?;

    {
        let mut child_slot = runtime
            .child
            .lock()
            .map_err(|_| "P2P runtime 锁不可用".to_string())?;
        if let Some(mut child) = child_slot.take() {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
    if let Some(pid) = read_p2p_runtime_pid(app)? {
        if is_process_running(pid) {
            kill_process_by_pid(pid)?;
        }
    }
    let _ = clear_p2p_runtime_pid(app);

    let stdout = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&stdout_log_path)
        .map_err(|err| format!("打开 EasyTier stdout 日志失败: {err}"))?;
    let stderr = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&stderr_log_path)
        .map_err(|err| format!("打开 EasyTier stderr 日志失败: {err}"))?;

    let mut command = background_command(&executable_path);
    command.current_dir(&work_dir);
    command.args(&args);
    command.stdout(Stdio::from(stdout));
    command.stderr(Stdio::from(stderr));

    let child = command
        .spawn()
        .map_err(|err| format!("启动 easytier-core 失败: {err}"))?;
    let pid = child.id();
    write_p2p_runtime_pid(app, pid)?;

    {
        let mut slot = runtime
            .child
            .lock()
            .map_err(|_| "P2P runtime 锁不可用".to_string())?;
        *slot = Some(child);
    }
    {
        let mut launch = runtime
            .launch
            .lock()
            .map_err(|_| "P2P runtime 锁不可用".to_string())?;
        *launch = P2PRuntimeLaunchState {
            available: true,
            configured: true,
            pid: Some(pid),
            started_at: Some(now_millis()),
            executable_path: executable_path.display().to_string(),
            work_dir: work_dir.display().to_string(),
            stdout_log_path: stdout_log_path.display().to_string(),
            stderr_log_path: stderr_log_path.display().to_string(),
            args_summary: summarize_p2p_args(&args),
            last_error: String::new(),
            machine_id: load_or_create_p2p_machine_id(app).unwrap_or_default(),
            rpc_portal: USER_P2P_RPC_PORTAL.to_string(),
            node_hostname: String::new(),
            virtual_ipv4: String::new(),
            instance_id: String::new(),
            peer_count: 0,
            connected_peers: Vec::new(),
        };
    }

    thread::sleep(Duration::from_millis(450));
    let status = current_p2p_runtime_status(app, runtime)?;
    if !status.running && !status.last_error.trim().is_empty() {
        return Err(status.last_error.clone());
    }
    Ok(status)
}

fn maybe_auto_start_p2p(app: &AppHandle) {
    let Ok(config) = load_app_config(app.clone()) else {
        return;
    };
    if !config.p2p_auto_start.unwrap_or(false) {
        return;
    }
    if let Some(runtime) = app.try_state::<P2PRuntimeManagerState>() {
        let _ = start_p2p_runtime_internal(app, &runtime);
    }
}

fn now_millis() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_millis() as u64)
        .unwrap_or(0)
}

fn open_path(path: PathBuf) {
    #[cfg(target_os = "windows")]
    let _ = Command::new("explorer").arg(path).spawn();
    #[cfg(target_os = "linux")]
    let _ = Command::new("xdg-open").arg(path).spawn();
    #[cfg(target_os = "macos")]
    let _ = Command::new("open").arg(path).spawn();
}

fn hide_to_tray(window: &Window) {
    if let Ok(pos) = window.outer_position() {
        let scale = window.scale_factor().unwrap_or(1.0);
        if let Ok(size) = window.inner_size() {
            let maximized = window.is_maximized().unwrap_or(false);
            let bounds = WindowBounds {
                x: pos.x,
                y: pos.y,
                width: (size.width as f64 / scale) as u32,
                height: (size.height as f64 / scale) as u32,
                maximized,
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
                let maximized = window.is_maximized().unwrap_or(false);
                let bounds = WindowBounds {
                    x: pos.x,
                    y: pos.y,
                    width: (size.width as f64 / scale) as u32,
                    height: (size.height as f64 / scale) as u32,
                    maximized,
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
    if let Some(runtime) = app.try_state::<P2PRuntimeManagerState>() {
        let _ = stop_p2p_runtime_process(app, &runtime);
    }
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
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|err| err.to_string())?
            .as_millis()
    );
    let window = WebviewWindowBuilder::new(app, label, WebviewUrl::App("index.html".into()))
        .title(&format!("驻阡陌用户端 - 客户端 {}", index))
        .inner_size(1100.0, 750.0)
        .min_inner_size(900.0, 600.0)
        .resizable(true)
        .decorations(false)
        .visible(false)
        .build()
        .map_err(|err| err.to_string())?;
    if let Ok(bounds) = load_window_bounds(app.clone()) {
        apply_window_bounds(&window, &bounds);
    }
    set_window_icon(&window);
    ensure_window_visible(&window);
    Ok(())
}

fn setup_tray(app: &AppHandle) -> tauri::Result<()> {
    let show = MenuItem::with_id(app, "show", "打开主窗口", true, None::<&str>)?;
    let open_logs = MenuItem::with_id(app, "open_logs", "打开日志目录", true, None::<&str>)?;
    let open_config = MenuItem::with_id(app, "open_config", "打开配置目录", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&show, &open_logs, &open_config, &quit])?;

    let tray_icon = Image::from_bytes(include_bytes!("../icons/icon-256.png"))?;
    TrayIconBuilder::new()
        .icon(tray_icon)
        .tooltip("驻阡陌用户端")
        .menu(&menu)
        .on_menu_event(move |app, event| match event.id.as_ref() {
            "show" => {
                if let Some(window) = app.get_webview_window("main") {
                    ensure_window_visible(&window);
                }
            }
            "open_logs" => {
                if let Ok(path) = app.path().app_log_dir() {
                    open_path(path);
                }
            }
            "open_config" => {
                if let Ok(path) = app.path().app_config_dir() {
                    open_path(path);
                }
            }
            "quit" => {
                if let Some(runtime) = app.try_state::<P2PRuntimeManagerState>() {
                    let _ = stop_p2p_runtime_process(app, &runtime);
                }
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
                if let Some(window) = tray.app_handle().get_webview_window("main") {
                    ensure_window_visible(&window);
                }
            }
        })
        .build(app)?;
    Ok(())
}

fn main() {
    let http_state = AppHttpState::new().expect("failed to create HTTP client");
    let installer_quit_on_launch =
        installer_requested_quit(&std::env::args().skip(1).collect::<Vec<_>>());
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, args, _| {
            if installer_requested_quit(&args) {
                handle_installer_quit_request(app);
                return;
            }
            request_second_launch_confirmation(app);
        }))
        .manage(http_state)
        .manage(P2PRuntimeManagerState::default())
        .setup(move |app| {
            ensure_config_dir(app.handle())
                .map_err(|err| -> Box<dyn std::error::Error> { err.into() })?;
            ensure_p2p_runtime_dirs(app.handle())
                .map_err(|err| -> Box<dyn std::error::Error> { err.into() })?;
            if installer_quit_on_launch {
                handle_installer_quit_request(app.handle());
                return Ok(());
            }
            setup_tray(app.handle())?;
            if let Some(window) = app.get_webview_window("main") {
                if let Ok(bounds) = load_window_bounds(app.handle().clone()) {
                    apply_window_bounds(&window, &bounds);
                }
                let _ = window.set_title("驻阡陌用户端");
                set_window_icon(&window);
                let _ = window.show();
            }
            maybe_auto_start_p2p(app.handle());
            Ok(())
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                match current_close_action(&window.app_handle()).as_str() {
                    "exit" => {
                        if let Some(runtime) =
                            window.app_handle().try_state::<P2PRuntimeManagerState>()
                        {
                            let _ = stop_p2p_runtime_process(&window.app_handle(), &runtime);
                        }
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
            p2p_runtime_status,
            p2p_runtime_start,
            p2p_runtime_stop,
            open_p2p_runtime_log,
            window_start_drag,
            window_minimize,
            window_toggle_maximize,
            window_request_close,
            app_exit,
            open_external,
            open_additional_window,
            set_auto_start,
            config_dir,
            log_dir,
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
        .expect("failed to build Cloud Relay User Console");

    let app_handle = app.handle().clone();
    let _ = app.run(move |_, event| {
        if let tauri::RunEvent::Exit = event {
            if let Some(runtime) = app_handle.try_state::<P2PRuntimeManagerState>() {
                let _ = stop_p2p_runtime_process(&app_handle, &runtime);
            }
        }
    });
}
