#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::collections::HashMap;
use std::fs::{self, OpenOptions};
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use std::time::Duration;
use std::time::{SystemTime, UNIX_EPOCH};

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use chrono::{Local, LocalResult, TimeZone};
use reqwest::blocking::Client;
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
        .setup(move |app| {
            ensure_runtime_dirs(app.handle())
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
            if let Some(runtime) = app_handle.try_state::<RuntimeManagerState>() {
                let _ = stop_runtime_process(&runtime);
            }
        }
    });
}
