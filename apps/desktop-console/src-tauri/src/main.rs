#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::collections::HashMap;
use std::ffi::OsStr;
use std::fs::{self, OpenOptions};
use std::io::{self, BufRead, BufReader, Read, Write};
use std::net::{Shutdown, TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::mpsc::{self, Sender};
use std::sync::Mutex;
use std::thread;
use std::thread::JoinHandle;
use std::time::Duration;
use std::time::{SystemTime, UNIX_EPOCH};

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use chrono::{Local, LocalResult, TimeZone};
use reqwest::blocking::{Body, Client};
use reqwest::header::{HeaderMap, HeaderName, HeaderValue};
use reqwest::Method;
use serde::{Deserialize, Serialize};
use tauri::image::Image;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Emitter, Manager, State, WebviewWindow, Window};
use uuid::Uuid;

#[cfg(target_os = "windows")]
use std::os::windows::process::CommandExt;
#[cfg(target_os = "windows")]
use windows_sys::Win32::Foundation::{FILETIME, HANDLE};
#[cfg(target_os = "windows")]
use windows_sys::Win32::NetworkManagement::IpHelper::{
    FreeMibTable, GetIfTable2, IF_TYPE_SOFTWARE_LOOPBACK, MIB_IF_ROW2, MIB_IF_TABLE2,
};
#[cfg(target_os = "windows")]
use windows_sys::Win32::NetworkManagement::Ndis::IfOperStatusUp;
#[cfg(target_os = "windows")]
use windows_sys::Win32::System::ProcessStatus::{K32GetProcessMemoryInfo, PROCESS_MEMORY_COUNTERS};
#[cfg(target_os = "windows")]
use windows_sys::Win32::System::Threading::{
    GetCurrentProcess, GetProcessIoCounters, GetProcessTimes, GetSystemTimes, IO_COUNTERS,
};

const CREATE_NO_WINDOW: u32 = 0x08000000;
const AGENT_RELATIVE_PATH: &str = "runtime/client-agent.exe";
const AGENT_RELAY_CONNECT_PATH: &str = "/agent/reverse-tcp";
const AGENT_UDP_RELAY_CONNECT_PATH: &str = "/agent/reverse-udp";
const AGENT_WEB_RELAY_CONNECT_PATH: &str = "/agent/reverse-web";
const INSTALLER_QUIT_ARG: &str = "--quit-for-install";
const P2P_RUNTIME_RELATIVE_PATH: &str = "runtime/easytier-core.exe";
const P2P_CLI_RELATIVE_PATH: &str = "runtime/easytier-cli.exe";
const PUBLISHER_P2P_TCP_LISTENER: &str = "tcp://0.0.0.0:21110";
const PUBLISHER_P2P_UDP_LISTENER: &str = "udp://0.0.0.0:21110";
const PUBLISHER_P2P_RPC_PORTAL: &str = "127.0.0.1:15888";
const MAX_HTTP_HEADER_BYTES: usize = 64 * 1024;

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
            .timeout(Duration::from_secs(8))
            .build()
            .map_err(|err| err.to_string())?;
        Ok(Self { client })
    }
}

#[derive(Default)]
struct ResourceSampleState {
    previous: Mutex<Option<CpuSample>>,
}

#[derive(Default)]
struct RuntimeManagerState {
    child: Mutex<Option<Child>>,
    launch: Mutex<RuntimeLaunchState>,
}

#[derive(Default)]
struct P2PRuntimeManagerState {
    child: Mutex<Option<Child>>,
    launch: Mutex<P2PRuntimeLaunchState>,
    op: Mutex<()>,
}

#[derive(Default)]
struct P2PServiceForwarderManagerState {
    handles: Mutex<HashMap<String, P2PServiceForwarderHandle>>,
}

#[derive(Clone, Default)]
struct RuntimeLaunchState {
    available: bool,
    pid: Option<u32>,
    started_at: Option<u64>,
    node_id: String,
    node_name: String,
    api_base_url: String,
    relay_tcp_url: String,
    relay_udp_url: String,
    executable_path: String,
    work_dir: String,
    stdout_log_path: String,
    stderr_log_path: String,
    last_error: String,
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

#[derive(Clone, Debug, PartialEq, Eq)]
struct P2PServiceForwarderSpec {
    tunnel_id: String,
    service_key: String,
    service_title: String,
    bind_host: String,
    listen_port: u16,
    target_host: String,
    target_port: u16,
    rewrite_host: String,
}

struct P2PServiceForwarderHandle {
    spec: P2PServiceForwarderSpec,
    shutdown: Sender<()>,
    join: Option<JoinHandle<()>>,
}

struct ParsedProxyRequest {
    method: String,
    target: String,
    headers: Vec<(String, String)>,
    content_length: Option<usize>,
    chunked: bool,
}

#[derive(Clone, Copy)]
struct CpuSample {
    process_ticks: u64,
    system_ticks: u64,
}

#[cfg(target_os = "windows")]
#[derive(Clone, Copy)]
struct NetSample {
    read_bytes: u64,
    write_bytes: u64,
}

#[derive(Debug, Deserialize)]
struct HostHttpRequestInput {
    url: String,
    method: Option<String>,
    headers: Option<HashMap<String, String>>,
    body: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct HostHttpResponse {
    status: u16,
    ok: bool,
    body: String,
    set_cookies: Vec<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct DesktopAppUsage {
    available: bool,
    cpu_percent: Option<f64>,
    memory_mb: Option<f64>,
    read_bytes: Option<u64>,
    write_bytes: Option<u64>,
    sampled_at: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct TunnelTrafficEntry {
    tunnel_id: String,
    down_bytes: u64,
    up_bytes: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct AgentTrafficSnapshot {
    tunnels: Vec<TunnelTrafficEntry>,
    down_total: u64,
    up_total: u64,
    sampled_at: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct TrafficDeltaInput {
    node_id: String,
    down_bytes: u64,
    up_bytes: u64,
    sampled_at: Option<u64>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase")]
struct TrafficHistoryDayEntry {
    date: String,
    month: String,
    down_bytes: u64,
    up_bytes: u64,
    total_bytes: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase")]
struct TrafficHistoryMonthEntry {
    month: String,
    down_bytes: u64,
    up_bytes: u64,
    total_bytes: u64,
    day_count: usize,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase")]
struct TrafficHistorySnapshot {
    node_id: String,
    current_month: String,
    days: Vec<TrafficHistoryDayEntry>,
    months: Vec<TrafficHistoryMonthEntry>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
struct TrafficLedgerDay {
    down_bytes: u64,
    up_bytes: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
struct TrafficLedgerFile {
    nodes: HashMap<String, HashMap<String, TrafficLedgerDay>>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct RuntimeStartInput {
    api_base_url: String,
    node_id: String,
    node_name: Option<String>,
    relay_tcp_url: Option<String>,
    relay_udp_url: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct RuntimeStatus {
    available: bool,
    running: bool,
    healthy: bool,
    pid: Option<u32>,
    started_at: Option<u64>,
    node_id: String,
    node_name: String,
    api_base_url: String,
    relay_tcp_url: String,
    relay_udp_url: String,
    executable_path: String,
    work_dir: String,
    stdout_log_path: String,
    stderr_log_path: String,
    last_error: String,
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

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
struct P2PServiceForwarderInput {
    tunnel_id: String,
    service_key: String,
    service_title: String,
    target_host: String,
    target_port: u16,
    listen_port: u16,
    rewrite_host: Option<String>,
}

#[tauri::command]
fn config_dir(app: AppHandle) -> Result<String, String> {
    let path = app.path().app_config_dir().map_err(|err| err.to_string())?;
    Ok(path.display().to_string())
}

#[tauri::command]
fn log_dir(app: AppHandle) -> Result<String, String> {
    let path = app.path().app_log_dir().map_err(|err| err.to_string())?;
    Ok(path.display().to_string())
}

#[tauri::command]
fn runtime_status(
    app: AppHandle,
    runtime: State<'_, RuntimeManagerState>,
    http: State<'_, AppHttpState>,
) -> Result<RuntimeStatus, String> {
    current_runtime_status(&app, &runtime, &http.client)
}

#[tauri::command]
fn runtime_start(
    app: AppHandle,
    runtime: State<'_, RuntimeManagerState>,
    http: State<'_, AppHttpState>,
    input: RuntimeStartInput,
) -> Result<RuntimeStatus, String> {
    ensure_runtime_dirs(&app)?;
    let executable_path = resolve_agent_executable(&app)?;
    let work_dir = runtime_work_dir(&app)?;
    let stdout_log_path = runtime_stdout_log_path(&app)?;
    let stderr_log_path = runtime_stderr_log_path(&app)?;
    let api_base_url = input.api_base_url.trim().trim_end_matches('/').to_string();
    if api_base_url.is_empty() {
        return Err("缺少云端 API 地址".to_string());
    }
    let node_name = input
        .node_name
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .unwrap_or("windows-publisher")
        .to_string();
    let persisted_node_id = ensure_runtime_node_id(&app, input.node_id.trim())?;
    let relay_tcp_url = input
        .relay_tcp_url
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_string)
        .unwrap_or_else(|| derive_relay_url(&api_base_url));
    let relay_udp_url = input
        .relay_udp_url
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_string)
        .unwrap_or_else(|| derive_udp_relay_url(&api_base_url));
    let relay_web_url = derive_web_relay_url(&api_base_url);

    stop_runtime_process(&runtime)?;

    let stdout = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&stdout_log_path)
        .map_err(|err| format!("打开 stdout 日志失败: {err}"))?;
    let stderr = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&stderr_log_path)
        .map_err(|err| format!("打开 stderr 日志失败: {err}"))?;

    let mut command = Command::new(&executable_path);
    command.current_dir(&work_dir);
    command.env("CLOUD_RELAY_API_URL", &api_base_url);
    command.env("RELAY_TCP_CONNECT_URL", &relay_tcp_url);
    command.env("RELAY_UDP_CONNECT_URL", &relay_udp_url);
    command.env("RELAY_WEB_CONNECT_URL", &relay_web_url);
    command.env("CLIENT_NODE_ID", &persisted_node_id);
    command.env("CLIENT_NODE_NAME", &node_name);
    command.env("CLIENT_DEPLOYMENT_MODE", "managed");
    command.env("CLIENT_SERVICE_UNIT", "desktop-embedded-runtime");
    command.env("AGENT_REVERSE_POOL_SIZE", "8");
    if let Ok(p2p_cli_path) = resolve_p2p_cli_executable(&app) {
        command.env("CLIENT_P2P_ASSIST", "true");
        command.env("CLIENT_P2P_CLI", p2p_cli_path);
        command.env("CLIENT_P2P_RPC_PORTAL", PUBLISHER_P2P_RPC_PORTAL);
        command.env("CLIENT_P2P_TIMEOUT_SEC", "3");
    }
    command.stdout(Stdio::from(stdout));
    command.stderr(Stdio::from(stderr));
    #[cfg(target_os = "windows")]
    command.creation_flags(CREATE_NO_WINDOW);

    let child = command
        .spawn()
        .map_err(|err| format!("启动本地发布运行时失败: {err}"))?;
    let pid = child.id();

    {
        let mut slot = runtime
            .child
            .lock()
            .map_err(|_| "runtime 锁不可用".to_string())?;
        *slot = Some(child);
    }
    {
        let mut launch = runtime
            .launch
            .lock()
            .map_err(|_| "runtime 锁不可用".to_string())?;
        *launch = RuntimeLaunchState {
            available: true,
            pid: Some(pid),
            started_at: Some(now_millis()),
            node_id: persisted_node_id,
            node_name,
            api_base_url,
            relay_tcp_url,
            relay_udp_url,
            executable_path: executable_path.display().to_string(),
            work_dir: work_dir.display().to_string(),
            stdout_log_path: stdout_log_path.display().to_string(),
            stderr_log_path: stderr_log_path.display().to_string(),
            last_error: String::new(),
        };
    }

    current_runtime_status(&app, &runtime, &http.client)
}

#[tauri::command]
fn runtime_stop(
    app: AppHandle,
    runtime: State<'_, RuntimeManagerState>,
    http: State<'_, AppHttpState>,
) -> Result<RuntimeStatus, String> {
    stop_runtime_process(&runtime)?;
    current_runtime_status(&app, &runtime, &http.client)
}

#[tauri::command]
fn open_runtime_log(
    app: AppHandle,
    runtime: State<'_, RuntimeManagerState>,
    kind: String,
) -> Result<(), String> {
    let launch = runtime
        .launch
        .lock()
        .map_err(|_| "runtime 锁不可用".to_string())?
        .clone();
    let path = match kind.as_str() {
        "stdout" => PathBuf::from(launch.stdout_log_path),
        "stderr" => PathBuf::from(launch.stderr_log_path),
        _ => return Err("未知日志类型".to_string()),
    };
    if path.as_os_str().is_empty() {
        return Err("日志文件尚未生成".to_string());
    }
    open_path(&app, path);
    Ok(())
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
    open_path(&app, path);
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
    if let Some(p2p_runtime) = app.try_state::<P2PRuntimeManagerState>() {
        let _ = stop_p2p_runtime_process(&app, &p2p_runtime);
    }
    save_bounds_on_exit(&app);
    app.exit(0);
    Ok(())
}

#[tauri::command]
fn auto_start_enabled() -> Result<bool, String> {
    let exe_path = std::env::current_exe()
        .map_err(|err| err.to_string())?
        .to_string_lossy()
        .to_string();
    let key = r"SOFTWARE\Microsoft\Windows\CurrentVersion\Run";
    let value_name = "ZhuQianMo";

    #[cfg(target_os = "windows")]
    {
        use windows_sys::Win32::System::Registry::*;
        let mut h_key: HKEY = core::ptr::null_mut();
        let result = unsafe {
            RegOpenKeyExW(
                HKEY_CURRENT_USER,
                encode_wide(key).as_ptr(),
                0,
                KEY_QUERY_VALUE,
                &mut h_key,
            )
        };
        if result == 2 {
            return Ok(false);
        }
        if result != 0 {
            return Err(format!("RegOpenKeyEx failed: {}", result));
        }

        let mut kind: u32 = 0;
        let mut byte_len: u32 = 0;
        let result = unsafe {
            RegQueryValueExW(
                h_key,
                encode_wide(value_name).as_ptr(),
                core::ptr::null_mut(),
                &mut kind,
                core::ptr::null_mut(),
                &mut byte_len,
            )
        };
        if result == 2 {
            unsafe { RegCloseKey(h_key) };
            return Ok(false);
        }
        if result != 0 {
            unsafe { RegCloseKey(h_key) };
            return Err(format!("RegQueryValueEx failed: {}", result));
        }
        if byte_len == 0 {
            unsafe { RegCloseKey(h_key) };
            return Ok(false);
        }

        let mut buffer = vec![0u16; (byte_len as usize / 2).max(1)];
        let result = unsafe {
            RegQueryValueExW(
                h_key,
                encode_wide(value_name).as_ptr(),
                core::ptr::null_mut(),
                &mut kind,
                buffer.as_mut_ptr() as *mut u8,
                &mut byte_len,
            )
        };
        unsafe { RegCloseKey(h_key) };
        if result != 0 {
            return Err(format!("RegQueryValueEx failed: {}", result));
        }

        let raw = String::from_utf16_lossy(&buffer)
            .trim_end_matches('\0')
            .trim()
            .trim_matches('"')
            .to_string();
        return Ok(raw.eq_ignore_ascii_case(&exe_path));
    }

    #[allow(unreachable_code)]
    Ok(false)
}

#[tauri::command]
fn set_auto_start(enable: bool) -> Result<(), String> {
    let exe_path = std::env::current_exe()
        .map_err(|err| err.to_string())?
        .to_string_lossy()
        .to_string();
    let key = r"SOFTWARE\Microsoft\Windows\CurrentVersion\Run";
    let value_name = "ZhuQianMo";

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

fn encode_wide(s: &str) -> Vec<u16> {
    s.encode_utf16().chain(std::iter::once(0u16)).collect()
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
    Err("当前平台未实现打开外部链接".to_string())
}

#[tauri::command]
fn http_request(
    state: State<'_, AppHttpState>,
    input: HostHttpRequestInput,
) -> Result<HostHttpResponse, String> {
    let method = input
        .method
        .as_deref()
        .unwrap_or("GET")
        .parse::<Method>()
        .map_err(|err| err.to_string())?;

    let mut request = state.client.request(method, &input.url);
    if let Some(headers) = input.headers {
        let mut header_map = HeaderMap::new();
        for (key, value) in headers {
            let name = HeaderName::from_bytes(key.as_bytes()).map_err(|err| err.to_string())?;
            let value = HeaderValue::from_str(&value).map_err(|err| err.to_string())?;
            header_map.insert(name, value);
        }
        request = request.headers(header_map);
    }
    if let Some(body) = input.body {
        request = request.body(body);
    }

    let response = request.send().map_err(|err| err.to_string())?;
    let status = response.status();
    let set_cookies = response
        .headers()
        .get_all(reqwest::header::SET_COOKIE)
        .iter()
        .filter_map(|value| value.to_str().ok().map(str::to_owned))
        .collect();
    let body = response.text().map_err(|err| err.to_string())?;
    Ok(HostHttpResponse {
        status: status.as_u16(),
        ok: status.is_success(),
        body,
        set_cookies,
    })
}

#[tauri::command]
fn app_resource_usage(state: State<'_, ResourceSampleState>) -> DesktopAppUsage {
    resource_usage_payload(&state)
}

#[tauri::command]
fn agent_traffic(runtime: State<'_, RuntimeManagerState>) -> Result<AgentTrafficSnapshot, String> {
    let launch = runtime
        .launch
        .lock()
        .map_err(|_| "runtime 锁不可用".to_string())?
        .clone();
    if launch.api_base_url.is_empty() {
        return Ok(AgentTrafficSnapshot {
            tunnels: vec![],
            down_total: 0,
            up_total: 0,
            sampled_at: now_millis() as i64,
        });
    }
    let url = format!("http://127.0.0.1:5180/agent/traffic");
    let client = reqwest::blocking::Client::builder()
        .timeout(std::time::Duration::from_millis(500))
        .build()
        .map_err(|err| err.to_string())?;
    let resp = client
        .get(&url)
        .send()
        .map_err(|err| format!("agent traffic 请求失败: {err}"))?;
    let snapshot: AgentTrafficSnapshot = resp
        .json()
        .map_err(|err| format!("agent traffic 解析失败: {err}"))?;
    Ok(snapshot)
}

#[tauri::command]
fn load_traffic_history(app: AppHandle, node_id: String) -> Result<TrafficHistorySnapshot, String> {
    let trimmed = node_id.trim().to_string();
    if trimmed.is_empty() {
        return Ok(empty_traffic_history_snapshot(String::new()));
    }
    let ledger = read_traffic_ledger_file(&app)?;
    let days = ledger.nodes.get(&trimmed).cloned().unwrap_or_default();
    Ok(build_traffic_history_snapshot(trimmed, days))
}

#[tauri::command]
fn record_traffic_delta(
    app: AppHandle,
    input: TrafficDeltaInput,
) -> Result<TrafficHistorySnapshot, String> {
    let node_id = input.node_id.trim().to_string();
    if node_id.is_empty() {
        return Ok(empty_traffic_history_snapshot(String::new()));
    }
    if input.down_bytes == 0 && input.up_bytes == 0 {
        return load_traffic_history(app, node_id);
    }

    let mut ledger = read_traffic_ledger_file(&app)?;
    let day_key = local_day_key_from_millis(input.sampled_at.unwrap_or_else(now_millis));
    let node_days = ledger.nodes.entry(node_id.clone()).or_default();
    let day = node_days.entry(day_key).or_default();
    day.down_bytes = day.down_bytes.saturating_add(input.down_bytes);
    day.up_bytes = day.up_bytes.saturating_add(input.up_bytes);
    write_traffic_ledger_file(&app, &ledger)?;

    let days = ledger.nodes.get(&node_id).cloned().unwrap_or_default();
    Ok(build_traffic_history_snapshot(node_id, days))
}

fn recent_log_excerpt(path: &str) -> String {
    let trimmed = path.trim();
    if trimmed.is_empty() {
        return String::new();
    }
    let Ok(content) = fs::read_to_string(trimmed) else {
        return String::new();
    };
    let lines: Vec<&str> = content
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty())
        .collect();
    if lines.is_empty() {
        return String::new();
    }
    lines
        .iter()
        .rev()
        .take(3)
        .copied()
        .collect::<Vec<&str>>()
        .into_iter()
        .rev()
        .collect::<Vec<&str>>()
        .join(" | ")
}

fn current_runtime_status(
    app: &AppHandle,
    runtime: &RuntimeManagerState,
    client: &Client,
) -> Result<RuntimeStatus, String> {
    ensure_runtime_dirs(app)?;
    let mut launch = runtime
        .launch
        .lock()
        .map_err(|_| "runtime 锁不可用".to_string())?;
    let mut child_slot = runtime
        .child
        .lock()
        .map_err(|_| "runtime 锁不可用".to_string())?;

    let available = resolve_agent_executable(app).is_ok();
    launch.available = available;

    let running = if let Some(child) = child_slot.as_mut() {
        match child.try_wait() {
            Ok(Some(status)) => {
                launch.pid = None;
                let stderr_excerpt = recent_log_excerpt(&launch.stderr_log_path);
                let mut detail = format!("本地发布运行时已退出: {status}");
                if !launch.stderr_log_path.trim().is_empty() {
                    detail.push_str(&format!(" / stderr: {}", launch.stderr_log_path));
                }
                if !stderr_excerpt.is_empty() {
                    detail.push_str(&format!(" / 摘要: {}", stderr_excerpt));
                }
                launch.last_error = detail;
                *child_slot = None;
                false
            }
            Ok(None) => {
                launch.pid = Some(child.id());
                true
            }
            Err(err) => {
                launch.last_error = format!("读取 runtime 状态失败: {err}");
                false
            }
        }
    } else {
        false
    };

    let healthy = running
        && !launch.api_base_url.is_empty()
        && client
            .get(format!("{}/api/auth/bootstrap-status", launch.api_base_url))
            .header("Accept", "application/json")
            .send()
            .map(|response| response.status().is_success())
            .unwrap_or(false);

    Ok(RuntimeStatus {
        available: launch.available,
        running,
        healthy,
        pid: launch.pid,
        started_at: launch.started_at,
        node_id: launch.node_id.clone(),
        node_name: launch.node_name.clone(),
        api_base_url: launch.api_base_url.clone(),
        relay_tcp_url: launch.relay_tcp_url.clone(),
        relay_udp_url: launch.relay_udp_url.clone(),
        executable_path: launch.executable_path.clone(),
        work_dir: launch.work_dir.clone(),
        stdout_log_path: launch.stdout_log_path.clone(),
        stderr_log_path: launch.stderr_log_path.clone(),
        last_error: launch.last_error.clone(),
    })
}

fn stop_runtime_process(runtime: &RuntimeManagerState) -> Result<(), String> {
    let mut child_slot = runtime
        .child
        .lock()
        .map_err(|_| "runtime 锁不可用".to_string())?;
    if let Some(mut child) = child_slot.take() {
        child
            .kill()
            .map_err(|err| format!("停止 runtime 失败: {err}"))?;
        let _ = child.wait();
    }
    let mut launch = runtime
        .launch
        .lock()
        .map_err(|_| "runtime 锁不可用".to_string())?;
    launch.pid = None;
    launch.last_error.clear();
    Ok(())
}

#[cfg(target_os = "windows")]
fn resource_usage_payload(state: &ResourceSampleState) -> DesktopAppUsage {
    let sampled_at = now_millis();
    let process = unsafe { GetCurrentProcess() };

    let mut memory = PROCESS_MEMORY_COUNTERS::default();
    memory.cb = std::mem::size_of::<PROCESS_MEMORY_COUNTERS>() as u32;
    let memory_ok = unsafe { K32GetProcessMemoryInfo(process, &mut memory, memory.cb) } != 0;

    let mut io = IO_COUNTERS::default();
    let process_io_ok = unsafe { GetProcessIoCounters(process, &mut io) } != 0;
    let network_sample = current_network_sample();

    let process_sample = current_cpu_sample(process);
    let cpu_percent = process_sample.and_then(|sample| {
        let mut guard = state.previous.lock().ok()?;
        let percent = guard.map(|previous| {
            let process_delta = sample.process_ticks.saturating_sub(previous.process_ticks);
            let system_delta = sample.system_ticks.saturating_sub(previous.system_ticks);
            if system_delta == 0 {
                0.0
            } else {
                ((process_delta as f64 / system_delta as f64) * 100.0 * logical_cpu_count() as f64)
                    .clamp(0.0, 100.0)
            }
        });
        *guard = Some(sample);
        percent
    });

    DesktopAppUsage {
        available: memory_ok || process_io_ok || cpu_percent.is_some() || network_sample.is_some(),
        cpu_percent,
        memory_mb: if memory_ok {
            Some(memory.WorkingSetSize as f64 / 1024.0 / 1024.0)
        } else {
            None
        },
        read_bytes: network_sample.map(|sample| sample.read_bytes).or_else(|| {
            if process_io_ok {
                Some(io.ReadTransferCount)
            } else {
                None
            }
        }),
        write_bytes: network_sample.map(|sample| sample.write_bytes).or_else(|| {
            if process_io_ok {
                Some(io.WriteTransferCount)
            } else {
                None
            }
        }),
        sampled_at,
    }
}

#[cfg(not(target_os = "windows"))]
fn resource_usage_payload(_state: &ResourceSampleState) -> DesktopAppUsage {
    DesktopAppUsage {
        available: false,
        cpu_percent: None,
        memory_mb: None,
        read_bytes: None,
        write_bytes: None,
        sampled_at: now_millis(),
    }
}

#[cfg(target_os = "windows")]
fn current_cpu_sample(process: HANDLE) -> Option<CpuSample> {
    let mut creation = FILETIME::default();
    let mut exit = FILETIME::default();
    let mut kernel = FILETIME::default();
    let mut user = FILETIME::default();
    let process_ok =
        unsafe { GetProcessTimes(process, &mut creation, &mut exit, &mut kernel, &mut user) } != 0;
    if !process_ok {
        return None;
    }

    let mut idle = FILETIME::default();
    let mut sys_kernel = FILETIME::default();
    let mut sys_user = FILETIME::default();
    let system_ok = unsafe { GetSystemTimes(&mut idle, &mut sys_kernel, &mut sys_user) } != 0;
    if !system_ok {
        return None;
    }

    Some(CpuSample {
        process_ticks: filetime_to_u64(kernel).saturating_add(filetime_to_u64(user)),
        system_ticks: filetime_to_u64(sys_kernel).saturating_add(filetime_to_u64(sys_user)),
    })
}

#[cfg(target_os = "windows")]
fn filetime_to_u64(value: FILETIME) -> u64 {
    ((value.dwHighDateTime as u64) << 32) | value.dwLowDateTime as u64
}

#[cfg(target_os = "windows")]
fn current_network_sample() -> Option<NetSample> {
    let mut table_ptr: *mut MIB_IF_TABLE2 = std::ptr::null_mut();
    let status = unsafe { GetIfTable2(&mut table_ptr) };
    if status != 0 || table_ptr.is_null() {
        return None;
    }

    let sample = unsafe {
        let table = &*table_ptr;
        let entries = std::slice::from_raw_parts(table.Table.as_ptr(), table.NumEntries as usize);
        let mut read_bytes = 0u64;
        let mut write_bytes = 0u64;
        for row in entries {
            if !network_row_usable(row) {
                continue;
            }
            read_bytes = read_bytes.saturating_add(row.InOctets);
            write_bytes = write_bytes.saturating_add(row.OutOctets);
        }
        NetSample {
            read_bytes,
            write_bytes,
        }
    };

    unsafe {
        FreeMibTable(table_ptr.cast());
    }
    Some(sample)
}

#[cfg(target_os = "windows")]
fn network_row_usable(row: &MIB_IF_ROW2) -> bool {
    row.OperStatus == IfOperStatusUp && row.Type != IF_TYPE_SOFTWARE_LOOPBACK
}

fn logical_cpu_count() -> usize {
    std::thread::available_parallelism()
        .map(|count| count.get())
        .unwrap_or(1)
}

fn ensure_window_visible(window: &WebviewWindow) {
    let _ = window.show();
    let _ = window.unminimize();
    let _ = window.set_focus();
}

fn open_path(app: &AppHandle, path: PathBuf) {
    #[cfg(target_os = "windows")]
    let _ = std::process::Command::new("explorer").arg(path).spawn();
    #[cfg(target_os = "linux")]
    let _ = std::process::Command::new("xdg-open").arg(path).spawn();
    #[cfg(target_os = "macos")]
    let _ = std::process::Command::new("open").arg(path).spawn();
    let _ = app;
}

fn notify_existing_instance(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.emit("app-already-open", "驻阡陌已经开启。");
    }
}

fn installer_requested_quit(args: &[String]) -> bool {
    args.iter().any(|arg| arg == INSTALLER_QUIT_ARG)
}

fn handle_installer_quit_request(app: &AppHandle) {
    if let Some(runtime) = app.try_state::<RuntimeManagerState>() {
        let _ = stop_runtime_process(&runtime);
    }
    save_bounds_on_exit(app);
    app.exit(0);
}

fn setup_tray(app: &AppHandle) -> tauri::Result<()> {
    let show_item = MenuItem::with_id(app, "show", "打开主窗口", true, None::<&str>)?;
    let open_logs_item = MenuItem::with_id(app, "open_logs", "打开日志目录", true, None::<&str>)?;
    let open_config_item =
        MenuItem::with_id(app, "open_config", "打开配置目录", true, None::<&str>)?;
    let quit_item = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
    let menu = Menu::with_items(
        app,
        &[&show_item, &open_logs_item, &open_config_item, &quit_item],
    )?;

    let app_handle = app.clone();
    let tray_icon = Image::from_bytes(include_bytes!("../icons/icon-256.png"))?;
    TrayIconBuilder::new()
        .icon(tray_icon)
        .tooltip("驻阡陌")
        .menu(&menu)
        .on_menu_event(move |app, event| match event.id.as_ref() {
            "show" => {
                if let Some(window) = app.get_webview_window("main") {
                    ensure_window_visible(&window);
                }
            }
            "open_logs" => {
                if let Ok(path) = app_handle.path().app_log_dir() {
                    open_path(&app_handle, path);
                }
            }
            "open_config" => {
                if let Ok(path) = app_handle.path().app_config_dir() {
                    open_path(&app_handle, path);
                }
            }
            "quit" => {
                if let Some(runtime) = app.try_state::<RuntimeManagerState>() {
                    let _ = stop_runtime_process(&runtime);
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

fn hide_to_tray(window: &Window) {
    save_bounds_on_exit(&window.app_handle());
    let _ = window.hide();
}

fn ensure_runtime_dirs(app: &AppHandle) -> Result<(), String> {
    for dir in [
        app.path().app_config_dir().map_err(|err| err.to_string())?,
        app.path().app_log_dir().map_err(|err| err.to_string())?,
        runtime_work_dir(app)?,
    ] {
        fs::create_dir_all(dir).map_err(|err| err.to_string())?;
    }
    Ok(())
}

fn runtime_work_dir(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|err| err.to_string())?
        .join("runtime"))
}

fn runtime_stdout_log_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_log_dir()
        .map_err(|err| err.to_string())?
        .join("runtime-stdout.log"))
}

fn runtime_stderr_log_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_log_dir()
        .map_err(|err| err.to_string())?
        .join("runtime-stderr.log"))
}

fn resolve_agent_executable(app: &AppHandle) -> Result<PathBuf, String> {
    // Portable layout: exe sits next to runtime-client-agent.exe
    let exe_dir = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|d| d.to_path_buf()));

    let mut candidates: Vec<PathBuf> = Vec::new();

    // 1. Portable: same directory as publisher exe
    if let Some(ref dir) = exe_dir {
        candidates.push(dir.join("runtime-client-agent.exe"));
        candidates.push(dir.join("runtime").join("client-agent.exe"));
    }

    // 2. Tauri resource dir (MSI install layout)
    if let Ok(res_dir) = app.path().resource_dir() {
        candidates.push(res_dir.join(AGENT_RELATIVE_PATH));
    }

    // 3. Dev-time fallbacks
    candidates.push(
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("runtime")
            .join("client-agent.exe"),
    );
    candidates.push(
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../../../deploy/bin/windows-amd64/client-agent.exe"),
    );

    for candidate in &candidates {
        if candidate.exists() {
            return Ok(candidate.clone());
        }
    }
    Err(format!(
        "未找到 bundled client-agent.exe (搜索了 {} 个路径)",
        candidates.len()
    ))
}

fn persisted_runtime_node_id_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(runtime_work_dir(app)?.join("node-id.txt"))
}

fn load_persisted_runtime_node_id(app: &AppHandle) -> Result<Option<String>, String> {
    let path = persisted_runtime_node_id_path(app)?;
    if !path.exists() {
        return Ok(None);
    }
    let value = fs::read_to_string(path).map_err(|err| err.to_string())?;
    let trimmed = value.trim();
    if trimmed.is_empty() {
        return Ok(None);
    }
    Ok(Some(trimmed.to_string()))
}

fn ensure_runtime_node_id(app: &AppHandle, requested: &str) -> Result<String, String> {
    let trimmed = requested.trim();
    if !trimmed.is_empty() {
        let path = persisted_runtime_node_id_path(app)?;
        fs::write(path, trimmed).map_err(|err| err.to_string())?;
        return Ok(trimmed.to_string());
    }
    if let Some(existing) = load_persisted_runtime_node_id(app)? {
        return Ok(existing);
    }
    let generated = format!("desktop-node-{}", Uuid::new_v4().simple());
    let path = persisted_runtime_node_id_path(app)?;
    fs::write(path, &generated).map_err(|err| err.to_string())?;
    Ok(generated)
}

fn derive_relay_url(api_base_url: &str) -> String {
    derive_relay_endpoint(api_base_url, 9090, AGENT_RELAY_CONNECT_PATH)
}

fn derive_udp_relay_url(api_base_url: &str) -> String {
    derive_relay_endpoint(api_base_url, 9093, AGENT_UDP_RELAY_CONNECT_PATH)
}

fn derive_web_relay_url(api_base_url: &str) -> String {
    derive_relay_endpoint(api_base_url, 9094, AGENT_WEB_RELAY_CONNECT_PATH)
}

fn derive_relay_endpoint(api_base_url: &str, port: u16, path: &str) -> String {
    reqwest::Url::parse(api_base_url)
        .ok()
        .map(|parsed| {
            let host = parsed.host_str().unwrap_or("127.0.0.1");
            format!("http://{}:{}{}", host, port, path)
        })
        .unwrap_or_else(|| format!("http://127.0.0.1:{}{}", port, path))
}

fn now_millis() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_millis() as u64)
        .unwrap_or(0)
}

fn local_hostname_fallback() -> String {
    std::env::var("COMPUTERNAME")
        .or_else(|_| std::env::var("HOSTNAME"))
        .unwrap_or_else(|_| "publisher-node".to_string())
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
    for dir in [
        app.path().app_config_dir().map_err(|err| err.to_string())?,
        app.path().app_log_dir().map_err(|err| err.to_string())?,
        p2p_runtime_work_dir(app)?,
    ] {
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
    let machine_id = format!("cloud-relay-publisher-{:016x}", fnv1a64(seed.as_bytes()));
    fs::write(&path, &machine_id).map_err(|err| err.to_string())?;
    Ok(machine_id)
}

fn read_p2p_runtime_pid(app: &AppHandle) -> Result<Option<u32>, String> {
    let path = p2p_runtime_pid_path(app)?;
    if !path.exists() {
        return Ok(None);
    }
    let content = fs::read_to_string(&path).map_err(|err| err.to_string())?;
    Ok(content.trim().parse::<u32>().ok())
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
        .args(["-p", PUBLISHER_P2P_RPC_PORTAL, "-o", "json"])
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
    launch.rpc_portal = PUBLISHER_P2P_RPC_PORTAL.to_string();
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
    let network_name = trim_option(config.p2p_network_name.as_deref())
        .unwrap_or_else(|| "cloud-relay".to_string());
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
        .unwrap_or_else(|| "cloud-relay-publisher".to_string());
    let hostname =
        trim_option(config.p2p_hostname.as_deref()).unwrap_or_else(local_hostname_fallback);
    let peers = {
        let configured = split_p2p_peers(config.p2p_peer_url.as_deref());
        if configured.is_empty() {
            vec![
                "tcp://easytier.manage.020309.top:11010".to_string(),
                "udp://easytier.manage.020309.top:11010".to_string(),
            ]
        } else {
            configured
        }
    };
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
        PUBLISHER_P2P_RPC_PORTAL.to_string(),
        "--file-log-dir".to_string(),
        file_log_dir.display().to_string(),
        "--listeners".to_string(),
        PUBLISHER_P2P_TCP_LISTENER.to_string(),
        "--listeners".to_string(),
        PUBLISHER_P2P_UDP_LISTENER.to_string(),
    ];
    if !hostname.trim().is_empty() {
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

fn normalize_p2p_bind_host(value: &str) -> String {
    value
        .trim()
        .split('/')
        .next()
        .unwrap_or_default()
        .trim()
        .to_string()
}

fn socket_addr(host: &str, port: u16) -> String {
    if host.contains(':') && !host.starts_with('[') {
        format!("[{host}]:{port}")
    } else {
        format!("{host}:{port}")
    }
}

fn build_forwarder_spec(
    bind_host: &str,
    item: &P2PServiceForwarderInput,
) -> Option<P2PServiceForwarderSpec> {
    if bind_host.trim().is_empty()
        || item.tunnel_id.trim().is_empty()
        || item.service_key.trim().is_empty()
        || item.listen_port == 0
        || item.target_port == 0
    {
        return None;
    }
    Some(P2PServiceForwarderSpec {
        tunnel_id: item.tunnel_id.trim().to_string(),
        service_key: item.service_key.trim().to_string(),
        service_title: item.service_title.trim().to_string(),
        bind_host: bind_host.trim().to_string(),
        listen_port: item.listen_port,
        target_host: if item.target_host.trim().is_empty() {
            "127.0.0.1".to_string()
        } else {
            item.target_host.trim().to_string()
        },
        target_port: item.target_port,
        rewrite_host: item
            .rewrite_host
            .as_deref()
            .unwrap_or_default()
            .trim()
            .to_string(),
    })
}

fn stop_p2p_service_forwarder(handle: P2PServiceForwarderHandle) {
    let _ = handle.shutdown.send(());
    if let Some(join) = handle.join {
        let _ = join.join();
    }
}

struct PrefixedReader<R> {
    prefix: Vec<u8>,
    pos: usize,
    inner: R,
}

impl<R> PrefixedReader<R> {
    fn new(prefix: Vec<u8>, inner: R) -> Self {
        Self {
            prefix,
            pos: 0,
            inner,
        }
    }
}

impl<R: Read> Read for PrefixedReader<R> {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        if self.pos < self.prefix.len() {
            let remaining = self.prefix.len() - self.pos;
            let count = remaining.min(buf.len());
            buf[..count].copy_from_slice(&self.prefix[self.pos..self.pos + count]);
            self.pos += count;
            return Ok(count);
        }
        self.inner.read(buf)
    }
}

fn read_http_header(stream: &mut TcpStream) -> io::Result<(Vec<u8>, Vec<u8>)> {
    let mut buffer = Vec::new();
    let mut chunk = [0_u8; 4096];
    loop {
        if let Some(position) = buffer.windows(4).position(|window| window == b"\r\n\r\n") {
            let split = position + 4;
            let body = buffer.split_off(split);
            return Ok((buffer, body));
        }
        if buffer.len() >= MAX_HTTP_HEADER_BYTES {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "http header too large",
            ));
        }
        let read = stream.read(&mut chunk)?;
        if read == 0 {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "http header truncated",
            ));
        }
        buffer.extend_from_slice(&chunk[..read]);
    }
}

fn read_line_crlf<R: BufRead>(reader: &mut R) -> io::Result<Vec<u8>> {
    let mut line = Vec::new();
    let read = reader.read_until(b'\n', &mut line)?;
    if read == 0 {
        return Err(io::Error::new(
            io::ErrorKind::UnexpectedEof,
            "unexpected eof while reading line",
        ));
    }
    Ok(line)
}

fn copy_exact_bytes<R: Read, W: Write>(
    reader: &mut R,
    writer: &mut W,
    mut remaining: usize,
) -> io::Result<()> {
    let mut buffer = [0_u8; 8192];
    while remaining > 0 {
        let chunk_len = buffer.len().min(remaining);
        let read = reader.read(&mut buffer[..chunk_len])?;
        if read == 0 {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "unexpected eof while copying body",
            ));
        }
        writer.write_all(&buffer[..read])?;
        remaining -= read;
    }
    Ok(())
}

fn copy_request_body(
    body_buffer: Vec<u8>,
    incoming: &mut TcpStream,
    outgoing: &mut TcpStream,
    content_length: Option<usize>,
    chunked: bool,
) -> io::Result<()> {
    if let Some(length) = content_length {
        let mut reader = PrefixedReader::new(body_buffer, incoming);
        return copy_exact_bytes(&mut reader, outgoing, length);
    }
    if chunked {
        let prefixed = PrefixedReader::new(body_buffer, incoming);
        let mut reader = BufReader::new(prefixed);
        loop {
            let size_line = read_line_crlf(&mut reader)?;
            outgoing.write_all(&size_line)?;
            let line = String::from_utf8_lossy(&size_line);
            let size_token = line.split(';').next().unwrap_or_default().trim();
            let chunk_size = usize::from_str_radix(size_token, 16)
                .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid chunk size"))?;
            if chunk_size == 0 {
                loop {
                    let trailer_line = read_line_crlf(&mut reader)?;
                    outgoing.write_all(&trailer_line)?;
                    if trailer_line == b"\r\n" {
                        return Ok(());
                    }
                }
            }
            copy_exact_bytes(&mut reader, outgoing, chunk_size + 2)?;
        }
    }
    // Requests without Content-Length / chunked should not forward any already-buffered bytes.
    // They may belong to a pipelined next request rather than the current request body.
    Ok(())
}

fn parse_proxy_request_header(header: &[u8]) -> io::Result<ParsedProxyRequest> {
    let header_text = String::from_utf8_lossy(header);
    let mut lines = header_text.split("\r\n");
    let request_line = lines.next().unwrap_or_default().trim_end_matches('\n');
    if request_line.is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "missing request line",
        ));
    }

    let mut parts = request_line.split_whitespace();
    let method = parts.next().unwrap_or_default().trim().to_string();
    let target = parts.next().unwrap_or_default().trim().to_string();
    if method.is_empty() || target.is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid request line",
        ));
    }

    let mut headers = Vec::new();
    let mut content_length = None;
    let mut chunked = false;

    for line in lines {
        if line.is_empty() {
            break;
        }
        if let Some((name, value)) = line.split_once(':') {
            let key = name.trim().to_string();
            let value = value.trim().to_string();
            if key.eq_ignore_ascii_case("content-length") {
                content_length = value.parse::<usize>().ok();
            }
            if key.eq_ignore_ascii_case("transfer-encoding")
                && value.to_ascii_lowercase().contains("chunked")
            {
                chunked = true;
            }
            headers.push((key, value));
        }
    }

    Ok(ParsedProxyRequest {
        method,
        target,
        headers,
        content_length,
        chunked,
    })
}

fn build_forwarder_target_url(
    spec: &P2PServiceForwarderSpec,
    target: &str,
) -> io::Result<reqwest::Url> {
    if let Ok(parsed) = reqwest::Url::parse(target) {
        let mut url = reqwest::Url::parse(&format!(
            "http://{}:{}/",
            spec.target_host, spec.target_port
        ))
        .map_err(|err| io::Error::new(io::ErrorKind::InvalidInput, err.to_string()))?;
        url.set_path(parsed.path());
        url.set_query(parsed.query());
        return Ok(url);
    }

    let normalized = if target.starts_with('/') {
        target.to_string()
    } else {
        format!("/{}", target)
    };
    reqwest::Url::parse(&format!(
        "http://{}:{}{}",
        spec.target_host, spec.target_port, normalized
    ))
    .map_err(|err| io::Error::new(io::ErrorKind::InvalidInput, err.to_string()))
}

fn should_skip_proxy_request_header(name: &str) -> bool {
    matches!(
        name,
        "host"
            | "connection"
            | "proxy-connection"
            | "content-length"
            | "transfer-encoding"
            | "expect"
    )
}

fn should_skip_proxy_response_header(name: &str) -> bool {
    matches!(name, "connection" | "transfer-encoding")
}

fn write_proxy_response_line(
    incoming: &mut TcpStream,
    status: reqwest::StatusCode,
) -> io::Result<()> {
    let reason = status.canonical_reason().unwrap_or("OK");
    incoming.write_all(format!("HTTP/1.1 {} {}\r\n", status.as_u16(), reason).as_bytes())
}

fn write_proxy_response_headers(
    incoming: &mut TcpStream,
    headers: &reqwest::header::HeaderMap,
    rewrite_host: &str,
    local_host: &str,
    local_port: u16,
) -> io::Result<()> {
    for (name, value) in headers {
        let lower = name.as_str().trim().to_ascii_lowercase();
        if should_skip_proxy_response_header(&lower) {
            continue;
        }
        let value_str = match value.to_str() {
            Ok(value) => value.trim(),
            Err(_) => continue,
        };
        let line = match lower.as_str() {
            "location" => format!(
                "{}: {}\r\n",
                name.as_str(),
                rewrite_absolute_url(value_str, rewrite_host, local_host, local_port)
            ),
            "refresh" => format!(
                "{}: {}\r\n",
                name.as_str(),
                rewrite_refresh_header(value_str, rewrite_host, local_host, local_port)
            ),
            "set-cookie" => format!(
                "{}: {}\r\n",
                name.as_str(),
                rewrite_set_cookie_header(value_str, rewrite_host)
            ),
            _ => format!("{}: {}\r\n", name.as_str(), value_str),
        };
        incoming.write_all(line.as_bytes())?;
    }
    incoming.write_all(b"Connection: close\r\n\r\n")
}

fn rewrite_absolute_url(
    value: &str,
    rewrite_host: &str,
    local_host: &str,
    local_port: u16,
) -> String {
    let trimmed = value.trim();
    if trimmed.is_empty() || rewrite_host.trim().is_empty() {
        return trimmed.to_string();
    }
    if let Ok(mut url) = reqwest::Url::parse(trimmed) {
        if url
            .host_str()
            .map(|host| host.eq_ignore_ascii_case(rewrite_host))
            .unwrap_or(false)
        {
            let _ = url.set_scheme("http");
            let _ = url.set_host(Some(local_host));
            let _ = url.set_port(Some(local_port));
            return url.to_string();
        }
    }
    if let Some(rest) = trimmed.strip_prefix("//") {
        let expected_prefix = format!("{}/", rewrite_host);
        if rest.eq_ignore_ascii_case(rewrite_host)
            || rest
                .to_ascii_lowercase()
                .starts_with(&expected_prefix.to_ascii_lowercase())
        {
            return format!("http://{}:{}", local_host, local_port) + &rest[rewrite_host.len()..];
        }
    }
    trimmed.to_string()
}

fn rewrite_request_absolute_url(
    value: &str,
    local_host: &str,
    local_port: u16,
    rewrite_host: &str,
) -> String {
    let trimmed = value.trim();
    if trimmed.is_empty() || rewrite_host.trim().is_empty() {
        return trimmed.to_string();
    }
    if let Ok(mut url) = reqwest::Url::parse(trimmed) {
        let parsed_port = url.port_or_known_default();
        if url
            .host_str()
            .map(|host| host.eq_ignore_ascii_case(local_host))
            .unwrap_or(false)
            && parsed_port == Some(local_port)
        {
            let _ = url.set_host(Some(rewrite_host));
            let _ = url.set_port(None);
            return url.to_string();
        }
    }
    trimmed.to_string()
}

fn rewrite_refresh_header(
    value: &str,
    rewrite_host: &str,
    local_host: &str,
    local_port: u16,
) -> String {
    let trimmed = value.trim();
    if let Some((prefix, url_value)) = trimmed.split_once("url=") {
        return format!(
            "{}url={}",
            prefix,
            rewrite_absolute_url(url_value, rewrite_host, local_host, local_port)
        );
    }
    trimmed.to_string()
}

fn rewrite_set_cookie_header(value: &str, rewrite_host: &str) -> String {
    let mut parts = value.split(';');
    let mut rebuilt = Vec::new();
    if let Some(first) = parts.next() {
        rebuilt.push(first.trim().to_string());
    }
    for part in parts {
        let trimmed = part.trim();
        if trimmed.eq_ignore_ascii_case("secure") {
            continue;
        }
        if let Some((name, attr_value)) = trimmed.split_once('=') {
            if name.trim().eq_ignore_ascii_case("domain")
                && attr_value
                    .trim()
                    .trim_start_matches('.')
                    .eq_ignore_ascii_case(rewrite_host)
            {
                continue;
            }
        }
        rebuilt.push(trimmed.to_string());
    }
    rebuilt.join("; ")
}

fn rewrite_http_request_header(
    header: &[u8],
    local_host: &str,
    local_port: u16,
    rewrite_host: &str,
) -> io::Result<(Vec<u8>, Option<usize>, bool)> {
    let header_text = String::from_utf8_lossy(header);
    let mut lines = header_text.split("\r\n");
    let request_line = lines.next().unwrap_or_default().trim_end_matches('\n');
    if request_line.is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "missing request line",
        ));
    }

    let mut rebuilt = Vec::new();
    rebuilt.push(request_line.to_string());
    let mut has_host = false;
    let mut has_connection = false;
    let mut content_length = None;
    let mut chunked = false;

    for line in lines {
        if line.is_empty() {
            break;
        }
        if let Some((name, value)) = line.split_once(':') {
            let lower = name.trim().to_ascii_lowercase();
            let value = value.trim();
            match lower.as_str() {
                "host" => {
                    has_host = true;
                    rebuilt.push(format!("Host: {}", rewrite_host));
                }
                "origin" | "referer" => rebuilt.push(format!(
                    "{}: {}",
                    name.trim(),
                    rewrite_request_absolute_url(value, local_host, local_port, rewrite_host)
                )),
                "connection" => {
                    has_connection = true;
                    rebuilt.push("Connection: close".to_string());
                }
                "proxy-connection" => {}
                "content-length" => {
                    content_length = value.parse::<usize>().ok();
                    rebuilt.push(format!("{}: {}", name.trim(), value));
                }
                "transfer-encoding" => {
                    if value.to_ascii_lowercase().contains("chunked") {
                        chunked = true;
                    }
                    rebuilt.push(format!("{}: {}", name.trim(), value));
                }
                _ => rebuilt.push(format!("{}: {}", name.trim(), value)),
            }
        }
    }

    if !has_host {
        rebuilt.push(format!("Host: {}", rewrite_host));
    }
    if !has_connection {
        rebuilt.push("Connection: close".to_string());
    }
    rebuilt.push(String::new());
    rebuilt.push(String::new());

    Ok((rebuilt.join("\r\n").into_bytes(), content_length, chunked))
}

fn rewrite_http_response_header(
    header: &[u8],
    rewrite_host: &str,
    local_host: &str,
    local_port: u16,
) -> io::Result<Vec<u8>> {
    let header_text = String::from_utf8_lossy(header);
    let mut lines = header_text.split("\r\n");
    let status_line = lines.next().unwrap_or_default().trim_end_matches('\n');
    if status_line.is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "missing response status line",
        ));
    }

    let mut rebuilt = Vec::new();
    rebuilt.push(status_line.to_string());
    let mut has_connection = false;

    for line in lines {
        if line.is_empty() {
            break;
        }
        if let Some((name, value)) = line.split_once(':') {
            let lower = name.trim().to_ascii_lowercase();
            let value = value.trim();
            match lower.as_str() {
                "location" => rebuilt.push(format!(
                    "{}: {}",
                    name.trim(),
                    rewrite_absolute_url(value, rewrite_host, local_host, local_port)
                )),
                "refresh" => rebuilt.push(format!(
                    "{}: {}",
                    name.trim(),
                    rewrite_refresh_header(value, rewrite_host, local_host, local_port)
                )),
                "set-cookie" => rebuilt.push(format!(
                    "{}: {}",
                    name.trim(),
                    rewrite_set_cookie_header(value, rewrite_host)
                )),
                "connection" => {
                    has_connection = true;
                    rebuilt.push("Connection: close".to_string());
                }
                _ => rebuilt.push(format!("{}: {}", name.trim(), value)),
            }
        }
    }

    if !has_connection {
        rebuilt.push("Connection: close".to_string());
    }
    rebuilt.push(String::new());
    rebuilt.push(String::new());

    Ok(rebuilt.join("\r\n").into_bytes())
}

fn bridge_http_connection_with_host_rewrite(
    mut incoming: TcpStream,
    spec: P2PServiceForwarderSpec,
) -> io::Result<()> {
    let (request_header, request_body_buffer) = read_http_header(&mut incoming)?;
    let parsed = parse_proxy_request_header(&request_header)?;
    let url = build_forwarder_target_url(&spec, &parsed.target)?;
    let method = parsed
        .method
        .parse::<Method>()
        .map_err(|err| io::Error::new(io::ErrorKind::InvalidInput, err.to_string()))?;
    let client = Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|err| io::Error::new(io::ErrorKind::Other, err.to_string()))?;

    let mut request = client
        .request(method, url)
        .header(reqwest::header::HOST, spec.rewrite_host.as_str())
        .header(reqwest::header::CONNECTION, "close");

    for (name, value) in parsed.headers {
        let lower = name.trim().to_ascii_lowercase();
        if should_skip_proxy_request_header(&lower) {
            continue;
        }
        let rewritten_value = match lower.as_str() {
            "origin" | "referer" => rewrite_request_absolute_url(
                &value,
                &spec.bind_host,
                spec.listen_port,
                &spec.rewrite_host,
            ),
            _ => value,
        };
        let header_name = HeaderName::from_bytes(name.as_bytes())
            .map_err(|err| io::Error::new(io::ErrorKind::InvalidInput, err.to_string()))?;
        let header_value = HeaderValue::from_str(&rewritten_value)
            .map_err(|err| io::Error::new(io::ErrorKind::InvalidInput, err.to_string()))?;
        request = request.header(header_name, header_value);
    }

    if let Some(length) = parsed.content_length {
        let reader = PrefixedReader::new(request_body_buffer, incoming.try_clone()?);
        request = request.body(Body::sized(reader, length as u64));
    } else if parsed.chunked {
        let reader = PrefixedReader::new(request_body_buffer, incoming.try_clone()?);
        request = request.body(Body::new(reader));
    }

    let mut response = request
        .send()
        .map_err(|err| io::Error::new(io::ErrorKind::Other, err.to_string()))?;
    let _ = incoming.set_nodelay(true);
    write_proxy_response_line(&mut incoming, response.status())?;
    write_proxy_response_headers(
        &mut incoming,
        response.headers(),
        &spec.rewrite_host,
        &spec.bind_host,
        spec.listen_port,
    )?;
    io::copy(&mut response, &mut incoming)?;
    let _ = incoming.flush();
    Ok(())
}

fn bridge_tcp_connection(mut incoming: TcpStream, spec: P2PServiceForwarderSpec) -> io::Result<()> {
    if !spec.rewrite_host.trim().is_empty() {
        return bridge_http_connection_with_host_rewrite(incoming, spec);
    }

    let target_addr = socket_addr(&spec.target_host, spec.target_port);
    let mut outgoing = TcpStream::connect(&target_addr)?;
    let _ = incoming.set_nodelay(true);
    let _ = outgoing.set_nodelay(true);

    let mut incoming_reader = incoming.try_clone()?;
    let mut outgoing_writer = outgoing.try_clone()?;
    let upstream = thread::spawn(move || {
        let _ = io::copy(&mut incoming_reader, &mut outgoing_writer);
        let _ = outgoing_writer.shutdown(Shutdown::Write);
    });

    let downstream = thread::spawn(move || {
        let _ = io::copy(&mut outgoing, &mut incoming);
        let _ = incoming.shutdown(Shutdown::Write);
    });

    let _ = upstream.join();
    let _ = downstream.join();
    Ok(())
}

fn run_p2p_service_forwarder(
    listener: TcpListener,
    shutdown_rx: std::sync::mpsc::Receiver<()>,
    spec: P2PServiceForwarderSpec,
) {
    loop {
        match shutdown_rx.try_recv() {
            Ok(_) | Err(std::sync::mpsc::TryRecvError::Disconnected) => break,
            Err(std::sync::mpsc::TryRecvError::Empty) => {}
        }

        match listener.accept() {
            Ok((stream, _)) => {
                let forward_spec = spec.clone();
                thread::spawn(move || {
                    let target = socket_addr(&forward_spec.target_host, forward_spec.target_port);
                    if let Err(err) = bridge_tcp_connection(stream, forward_spec) {
                        eprintln!("p2p service forwarder connect {target} failed: {err}");
                    }
                });
            }
            Err(err) if err.kind() == io::ErrorKind::WouldBlock => {
                thread::sleep(Duration::from_millis(150));
            }
            Err(err) => {
                eprintln!(
                    "p2p service forwarder {} accept failed: {}",
                    socket_addr(&spec.bind_host, spec.listen_port),
                    err
                );
                thread::sleep(Duration::from_millis(250));
            }
        }
    }
}

fn start_p2p_service_forwarder(
    spec: P2PServiceForwarderSpec,
) -> Result<P2PServiceForwarderHandle, String> {
    let bind_addr = socket_addr(&spec.bind_host, spec.listen_port);
    let listener = TcpListener::bind(&bind_addr)
        .map_err(|err| format!("P2P 服务监听失败 {bind_addr}: {err}"))?;
    listener
        .set_nonblocking(true)
        .map_err(|err| format!("P2P 服务监听初始化失败 {bind_addr}: {err}"))?;
    let (shutdown_tx, shutdown_rx) = mpsc::channel();
    let thread_spec = spec.clone();
    let join = thread::spawn(move || run_p2p_service_forwarder(listener, shutdown_rx, thread_spec));
    Ok(P2PServiceForwarderHandle {
        spec,
        shutdown: shutdown_tx,
        join: Some(join),
    })
}

#[tauri::command]
fn sync_p2p_service_forwarders(
    app: AppHandle,
    runtime: State<P2PRuntimeManagerState>,
    forwarders: State<P2PServiceForwarderManagerState>,
    items: Vec<P2PServiceForwarderInput>,
) -> Result<(), String> {
    let status = current_p2p_runtime_status(&app, &runtime)?;
    let bind_host = if status.running {
        normalize_p2p_bind_host(&status.virtual_ipv4)
    } else {
        String::new()
    };

    let desired_specs = items
        .iter()
        .filter_map(|item| build_forwarder_spec(&bind_host, item))
        .map(|spec| (spec.tunnel_id.clone(), spec))
        .collect::<HashMap<String, P2PServiceForwarderSpec>>();

    let mut handles = forwarders
        .handles
        .lock()
        .map_err(|_| "P2P 服务转发器锁不可用".to_string())?;

    let existing_ids = handles.keys().cloned().collect::<Vec<String>>();
    let mut to_stop = Vec::new();
    for tunnel_id in existing_ids {
        let should_keep = desired_specs
            .get(&tunnel_id)
            .map(|desired| {
                handles
                    .get(&tunnel_id)
                    .map(|current| current.spec == *desired)
                    .unwrap_or(false)
            })
            .unwrap_or(false);
        if !should_keep {
            if let Some(handle) = handles.remove(&tunnel_id) {
                to_stop.push(handle);
            }
        }
    }
    drop(handles);

    for handle in to_stop {
        stop_p2p_service_forwarder(handle);
    }

    let mut handles = forwarders
        .handles
        .lock()
        .map_err(|_| "P2P 服务转发器锁不可用".to_string())?;

    for (tunnel_id, spec) in desired_specs {
        if handles.contains_key(&tunnel_id) {
            continue;
        }
        let handle = start_p2p_service_forwarder(spec)?;
        handles.insert(tunnel_id, handle);
    }

    Ok(())
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
            rpc_portal: PUBLISHER_P2P_RPC_PORTAL.to_string(),
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

// ─── Login Profiles ───

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
    fn LocalFree(hMem: *mut core::ffi::c_void) -> *mut core::ffi::c_void;
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

fn login_profiles_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|err| err.to_string())?
        .join("login-profiles.json"))
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

    // Upsert by email
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

// ─── Window State & App Config ───

#[derive(Serialize, Deserialize, Default)]
struct WindowBounds {
    x: i32,
    y: i32,
    width: u32,
    height: u32,
    maximized: bool,
}

#[derive(Serialize, Deserialize, Default)]
struct AppConfig {
    #[serde(rename = "closeAction")]
    close_action: Option<String>, // "ask" | "tray" | "exit"
    #[serde(rename = "silentStart")]
    silent_start: Option<bool>,
    #[serde(rename = "autoStart")]
    auto_start: Option<bool>,
    #[serde(rename = "p2pAutoStart")]
    p2p_auto_start: Option<bool>,
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

fn window_state_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|err| err.to_string())?
        .join("window-state.json"))
}

fn app_config_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|err| err.to_string())?
        .join("app-config.json"))
}

fn traffic_ledger_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|err| err.to_string())?
        .join("traffic-ledger.json"))
}

#[tauri::command]
fn save_window_bounds(app: AppHandle, bounds: WindowBounds) -> Result<(), String> {
    let path = window_state_path(&app)?;
    let content = serde_json::to_string_pretty(&bounds).map_err(|err| err.to_string())?;
    fs::write(&path, content).map_err(|err| err.to_string())
}

#[tauri::command]
fn load_window_bounds(app: AppHandle) -> Result<WindowBounds, String> {
    let path = window_state_path(&app)?;
    if !path.exists() {
        return Ok(WindowBounds::default());
    }
    let content = fs::read_to_string(&path).map_err(|err| err.to_string())?;
    serde_json::from_str(&content).map_err(|err| err.to_string())
}

#[tauri::command]
fn save_app_config(app: AppHandle, config: AppConfig) -> Result<(), String> {
    let path = app_config_path(&app)?;
    let content = serde_json::to_string_pretty(&config).map_err(|err| err.to_string())?;
    fs::write(&path, content).map_err(|err| err.to_string())
}

#[tauri::command]
fn load_app_config(app: AppHandle) -> Result<AppConfig, String> {
    let path = app_config_path(&app)?;
    if !path.exists() {
        return Ok(AppConfig::default());
    }
    let content = fs::read_to_string(&path).map_err(|err| err.to_string())?;
    serde_json::from_str(&content).map_err(|err| err.to_string())
}

fn read_traffic_ledger_file(app: &AppHandle) -> Result<TrafficLedgerFile, String> {
    let path = traffic_ledger_path(app)?;
    if !path.exists() {
        return Ok(TrafficLedgerFile::default());
    }
    let content = fs::read_to_string(&path).map_err(|err| err.to_string())?;
    serde_json::from_str(&content).map_err(|err| err.to_string())
}

fn write_traffic_ledger_file(app: &AppHandle, data: &TrafficLedgerFile) -> Result<(), String> {
    let path = traffic_ledger_path(app)?;
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|err| err.to_string())?;
    }
    let content = serde_json::to_string_pretty(data).map_err(|err| err.to_string())?;
    fs::write(&path, content).map_err(|err| err.to_string())
}

fn local_day_key_from_millis(sampled_at: u64) -> String {
    match Local.timestamp_millis_opt(sampled_at as i64) {
        LocalResult::Single(value) => value.format("%Y-%m-%d").to_string(),
        _ => Local::now().format("%Y-%m-%d").to_string(),
    }
}

fn current_month_key() -> String {
    Local::now().format("%Y-%m").to_string()
}

fn empty_traffic_history_snapshot(node_id: String) -> TrafficHistorySnapshot {
    TrafficHistorySnapshot {
        node_id,
        current_month: current_month_key(),
        days: Vec::new(),
        months: Vec::new(),
    }
}

fn build_traffic_history_snapshot(
    node_id: String,
    days: HashMap<String, TrafficLedgerDay>,
) -> TrafficHistorySnapshot {
    if days.is_empty() {
        return empty_traffic_history_snapshot(node_id);
    }

    let mut day_items = days
        .into_iter()
        .map(|(date, totals)| TrafficHistoryDayEntry {
            month: date.get(..7).unwrap_or_default().to_string(),
            total_bytes: totals.down_bytes.saturating_add(totals.up_bytes),
            down_bytes: totals.down_bytes,
            up_bytes: totals.up_bytes,
            date,
        })
        .collect::<Vec<_>>();
    day_items.sort_by(|left, right| left.date.cmp(&right.date));

    let mut month_map: HashMap<String, TrafficHistoryMonthEntry> = HashMap::new();
    for day in &day_items {
        let month_entry =
            month_map
                .entry(day.month.clone())
                .or_insert_with(|| TrafficHistoryMonthEntry {
                    month: day.month.clone(),
                    down_bytes: 0,
                    up_bytes: 0,
                    total_bytes: 0,
                    day_count: 0,
                });
        month_entry.down_bytes = month_entry.down_bytes.saturating_add(day.down_bytes);
        month_entry.up_bytes = month_entry.up_bytes.saturating_add(day.up_bytes);
        month_entry.total_bytes = month_entry.total_bytes.saturating_add(day.total_bytes);
        month_entry.day_count += 1;
    }

    let mut month_items = month_map.into_values().collect::<Vec<_>>();
    month_items.sort_by(|left, right| right.month.cmp(&left.month));

    TrafficHistorySnapshot {
        node_id,
        current_month: current_month_key(),
        days: day_items,
        months: month_items,
    }
}

fn main() {
    let http_state = AppHttpState::new().expect("failed to create desktop HTTP client");
    let installer_quit_on_launch =
        installer_requested_quit(&std::env::args().skip(1).collect::<Vec<_>>());
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, args, _| {
            if installer_requested_quit(&args) {
                handle_installer_quit_request(app);
                return;
            }
            notify_existing_instance(app);
        }))
        .manage(http_state)
        .manage(ResourceSampleState::default())
        .manage(RuntimeManagerState::default())
        .manage(P2PRuntimeManagerState::default())
        .manage(P2PServiceForwarderManagerState::default())
        .setup(move |app| {
            ensure_runtime_dirs(app.handle())
                .map_err(|err| -> Box<dyn std::error::Error> { err.into() })?;
            ensure_p2p_runtime_dirs(app.handle())
                .map_err(|err| -> Box<dyn std::error::Error> { err.into() })?;
            if installer_quit_on_launch {
                handle_installer_quit_request(app.handle());
                return Ok(());
            }
            setup_tray(app.handle())?;
            // Restore saved window bounds, then show window (avoids flash at default position)
            if let Some(window) = app.get_webview_window("main") {
                // Set high-res window icon (taskbar icon)
                let window_icon = Image::from_bytes(include_bytes!("../icons/icon-256.png"))?;
                let _ = window.set_icon(window_icon);
                if let Ok(bounds) = load_window_bounds(app.handle().clone()) {
                    if bounds.width > 0 {
                        let _ = window.set_size(tauri::LogicalSize::new(
                            bounds.width as f64,
                            bounds.height as f64,
                        ));
                        let _ = window.set_position(tauri::Position::Logical(
                            tauri::LogicalPosition::new(bounds.x as f64, bounds.y as f64),
                        ));
                        if bounds.maximized {
                            let _ = window.maximize();
                        }
                    }
                }
                let _ = window.show();
            }
            let app_handle = app.handle().clone();
            thread::spawn(move || {
                thread::sleep(Duration::from_millis(80));
                maybe_auto_start_p2p(&app_handle);
            });
            Ok(())
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.emit("app-close-requested", ());
            }
        })
        .invoke_handler(tauri::generate_handler![
            config_dir,
            log_dir,
            runtime_status,
            runtime_start,
            runtime_stop,
            open_runtime_log,
            p2p_runtime_status,
            p2p_runtime_start,
            p2p_runtime_stop,
            open_p2p_runtime_log,
            sync_p2p_service_forwarders,
            window_start_drag,
            window_minimize,
            window_toggle_maximize,
            window_request_close,
            app_exit,
            auto_start_enabled,
            set_auto_start,
            open_external,
            http_request,
            app_resource_usage,
            agent_traffic,
            load_traffic_history,
            record_traffic_delta,
            read_login_profiles,
            save_login_profile,
            delete_login_profile,
            decrypt_login_password,
            save_window_bounds,
            load_window_bounds,
            save_app_config,
            load_app_config
        ])
        .build(tauri::generate_context!())
        .expect("failed to build Cloud Relay Publisher");

    let app_handle = app.handle().clone();
    let _ = app.run(move |_, event| {
        if let tauri::RunEvent::Exit = event {
            if let Some(forwarders) = app_handle.try_state::<P2PServiceForwarderManagerState>() {
                if let Ok(mut handles) = forwarders.handles.lock() {
                    let drained = handles
                        .drain()
                        .map(|(_, handle)| handle)
                        .collect::<Vec<_>>();
                    drop(handles);
                    for handle in drained {
                        stop_p2p_service_forwarder(handle);
                    }
                }
            }
            if let Some(p2p_runtime) = app_handle.try_state::<P2PRuntimeManagerState>() {
                let _ = stop_p2p_runtime_process(&app_handle, &p2p_runtime);
            }
            if let Some(runtime) = app_handle.try_state::<RuntimeManagerState>() {
                let _ = stop_runtime_process(&runtime);
            }
        }
    });
}
