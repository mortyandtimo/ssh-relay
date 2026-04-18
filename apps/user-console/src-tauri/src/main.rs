#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::collections::HashMap;
use std::ffi::OsStr;
use std::fs::{self, OpenOptions};
use std::io::{self, BufRead, BufReader, Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{self, Sender};
use std::sync::Mutex;
use std::thread;
use std::thread::JoinHandle;
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
const DEFAULT_API_BASE_URL: &str = "https://manage.020309.top";
const P2P_RUNTIME_RELATIVE_PATH: &str = "runtime/easytier-core.exe";
const P2P_CLI_RELATIVE_PATH: &str = "runtime/easytier-cli.exe";
const USER_P2P_TCP_LISTENER: &str = "tcp://0.0.0.0:21010";
const USER_P2P_UDP_LISTENER: &str = "udp://0.0.0.0:21010";
const USER_P2P_RPC_PORTAL: &str = "127.0.0.1:29888";
const USER_NODE_HEARTBEAT_SEC: u64 = 30;
const WORKSPACE_PROXY_BIND_HOST: &str = "127.0.0.1";
const MAX_HTTP_HEADER_BYTES: usize = 64 * 1024;
const WORKSPACE_PROXY_LOG_FILE: &str = "workspace-proxy.log";

static WORKSPACE_PROXY_TRACE_ID: AtomicU64 = AtomicU64::new(1);

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

#[derive(Default)]
struct UserNodeAgentState {
    identity: Mutex<UserNodeIdentityState>,
    launch: Mutex<UserNodeLaunchState>,
    op: Mutex<()>,
}

#[derive(Default)]
struct ServiceWorkspaceProxyManagerState {
    handles: Mutex<HashMap<String, ServiceWorkspaceProxyHandle>>,
}

#[derive(Clone, Default)]
struct UserNodeIdentityState {
    email: String,
    role: String,
    display_name: String,
}

#[derive(Clone, Default)]
struct UserNodeLaunchState {
    enabled: bool,
    registered: bool,
    online: bool,
    node_id: String,
    node_name: String,
    api_base_url: String,
    owner_email: String,
    owner_role: String,
    last_register_at: Option<u64>,
    last_heartbeat_at: Option<u64>,
    recommended_heartbeat_sec: u64,
    p2p_running: bool,
    p2p_virtual_ipv4: String,
    p2p_peer_count: usize,
    p2p_hostname: String,
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
struct ServiceWorkspaceProxySpec {
    key: String,
    bind_host: String,
    listen_port: u16,
    upstream_scheme: String,
    upstream_host: String,
    upstream_port: u16,
    launch_path_and_query: String,
    log_path: PathBuf,
}

struct ServiceWorkspaceProxyHandle {
    spec: ServiceWorkspaceProxySpec,
    shutdown: Sender<()>,
    join: Option<JoinHandle<()>>,
}

struct ParsedWorkspaceRequest {
    method: String,
    target: String,
    headers: Vec<(String, String)>,
    content_length: Option<usize>,
    chunked: bool,
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

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct UserNodeStatus {
    enabled: bool,
    registered: bool,
    online: bool,
    node_id: String,
    node_name: String,
    api_base_url: String,
    owner_email: String,
    owner_role: String,
    last_register_at: Option<u64>,
    last_heartbeat_at: Option<u64>,
    recommended_heartbeat_sec: u64,
    p2p_running: bool,
    p2p_virtual_ipv4: String,
    p2p_peer_count: usize,
    p2p_hostname: String,
    last_error: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct UserNodeIdentityInput {
    email: Option<String>,
    role: Option<String>,
    display_name: Option<String>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct ServiceWorkspaceInput {
    key: String,
    title: String,
    url: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct ServiceWorkspaceProbeInput {
    url: String,
    host_header: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct ServiceWorkspaceProbeOutput {
    reachable: bool,
    status: Option<u16>,
    final_url: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct AgentNodeCapabilities {
    tcp_relay: bool,
    http_relay: bool,
    https_relay: bool,
    udp_relay: bool,
    p2p_assist: bool,
    socks5_connect: bool,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct AgentNodeRegisterRequest {
    node_id: String,
    node_name: String,
    agent_version: String,
    capabilities: AgentNodeCapabilities,
    metadata: HashMap<String, String>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct AgentNodeRegisterResponse {
    node_id: String,
    recommended_heartbeat_sec: Option<u64>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct AgentNodeHeartbeatRequest {
    node_id: String,
    metrics: HashMap<String, String>,
    observed_at: String,
    active_tunnels: i32,
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
fn user_node_status(state: State<'_, UserNodeAgentState>) -> Result<UserNodeStatus, String> {
    current_user_node_status(&state)
}

#[tauri::command]
fn user_node_sync(
    app: AppHandle,
    runtime: State<'_, P2PRuntimeManagerState>,
    node_state: State<'_, UserNodeAgentState>,
    identity: Option<UserNodeIdentityInput>,
) -> Result<UserNodeStatus, String> {
    if let Some(identity) = identity {
        update_user_node_identity(&node_state, identity)?;
    }
    sync_user_node_registration(&app, &runtime, &node_state)
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
    stop_all_service_workspace_proxies(&app);
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
fn open_service_workspace_external(
    app: AppHandle,
    input: ServiceWorkspaceInput,
) -> Result<(), String> {
    let key = if input.key.trim().is_empty() {
        "service".to_string()
    } else {
        sanitize_node_token(input.key.trim())
    };
    let upstream_url = reqwest::Url::parse(input.url.trim()).map_err(|err| err.to_string())?;
    let local_url = ensure_service_workspace_proxy(&app, &key, &upstream_url)?;
    open_external(local_url)
}

fn socket_addr(host: &str, port: u16) -> String {
    if host.contains(':') && !host.starts_with('[') {
        format!("[{host}]:{port}")
    } else {
        format!("{host}:{port}")
    }
}

fn workspace_proxy_log_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_log_dir()
        .map_err(|err| err.to_string())?
        .join(WORKSPACE_PROXY_LOG_FILE))
}

fn next_workspace_proxy_trace_id() -> u64 {
    WORKSPACE_PROXY_TRACE_ID.fetch_add(1, Ordering::Relaxed)
}

fn sanitize_proxy_log_value(value: &str) -> String {
    value
        .replace('\r', "\\r")
        .replace('\n', "\\n")
        .trim()
        .to_string()
}

fn append_proxy_log_line(log_path: &Path, scope: &str, trace_id: u64, message: &str) {
    let millis = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_millis())
        .unwrap_or_default();
    if let Some(parent) = log_path.parent() {
        let _ = fs::create_dir_all(parent);
    }
    let Ok(mut file) = OpenOptions::new().create(true).append(true).open(log_path) else {
        return;
    };
    let _ = writeln!(
        file,
        "[{millis}] [{scope}] [#{trace_id}] {}",
        sanitize_proxy_log_value(message)
    );
}

fn workspace_header_value(headers: &[(String, String)], name: &str) -> String {
    headers
        .iter()
        .rev()
        .find(|(key, _)| key.eq_ignore_ascii_case(name))
        .map(|(_, value)| value.clone())
        .unwrap_or_default()
}

fn workspace_proxy_spec_from_url(
    key: &str,
    url: &reqwest::Url,
) -> Result<ServiceWorkspaceProxySpec, String> {
    let scheme = url.scheme().trim().to_ascii_lowercase();
    if scheme != "http" {
        return Err(format!("暂不支持的工作台协议: {}", url.scheme()));
    }
    let upstream_host = url
        .host_str()
        .map(str::to_string)
        .ok_or_else(|| "服务工作台 URL 缺少 host".to_string())?;
    let upstream_port = url
        .port_or_known_default()
        .ok_or_else(|| "服务工作台 URL 缺少端口".to_string())?;
    let mut launch_path_and_query = url.path().to_string();
    if launch_path_and_query.is_empty() {
        launch_path_and_query = "/".to_string();
    }
    if let Some(query) = url.query() {
        launch_path_and_query.push('?');
        launch_path_and_query.push_str(query);
    }
    Ok(ServiceWorkspaceProxySpec {
        key: key.to_string(),
        bind_host: WORKSPACE_PROXY_BIND_HOST.to_string(),
        listen_port: 0,
        upstream_scheme: scheme,
        upstream_host,
        upstream_port,
        launch_path_and_query,
        log_path: PathBuf::new(),
    })
}

fn stop_service_workspace_proxy(handle: ServiceWorkspaceProxyHandle) {
    let _ = handle.shutdown.send(());
    if let Some(join) = handle.join {
        let _ = join.join();
    }
}

fn stop_all_service_workspace_proxies(app: &AppHandle) {
    let Some(manager) = app.try_state::<ServiceWorkspaceProxyManagerState>() else {
        return;
    };
    let handles = {
        let Ok(mut guard) = manager.handles.lock() else {
            return;
        };
        guard.drain().map(|(_, handle)| handle).collect::<Vec<_>>()
    };
    for handle in handles {
        stop_service_workspace_proxy(handle);
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

struct ChunkedBodyReader<R> {
    inner: R,
    remaining_in_chunk: usize,
    finished: bool,
}

impl<R> ChunkedBodyReader<R> {
    fn new(inner: R) -> Self {
        Self {
            inner,
            remaining_in_chunk: 0,
            finished: false,
        }
    }
}

impl<R: BufRead> ChunkedBodyReader<R> {
    fn read_next_chunk_size(&mut self) -> io::Result<()> {
        if self.finished {
            return Ok(());
        }
        let size_line = read_line_crlf(&mut self.inner)?;
        let line = String::from_utf8_lossy(&size_line);
        let size_token = line.split(';').next().unwrap_or_default().trim();
        let chunk_size = usize::from_str_radix(size_token, 16)
            .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid chunk size"))?;
        if chunk_size == 0 {
            loop {
                let trailer_line = read_line_crlf(&mut self.inner)?;
                if trailer_line == b"\r\n" {
                    self.finished = true;
                    return Ok(());
                }
            }
        }
        self.remaining_in_chunk = chunk_size;
        Ok(())
    }

    fn consume_chunk_suffix(&mut self) -> io::Result<()> {
        let mut suffix = [0_u8; 2];
        self.inner.read_exact(&mut suffix)?;
        if suffix != *b"\r\n" {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "invalid chunk suffix",
            ));
        }
        Ok(())
    }
}

impl<R: BufRead> Read for ChunkedBodyReader<R> {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        if buf.is_empty() {
            return Ok(0);
        }
        loop {
            if self.finished {
                return Ok(0);
            }
            if self.remaining_in_chunk == 0 {
                self.read_next_chunk_size()?;
                if self.finished {
                    return Ok(0);
                }
            }
            let limit = buf.len().min(self.remaining_in_chunk);
            let read = self.inner.read(&mut buf[..limit])?;
            if read == 0 {
                return Err(io::Error::new(
                    io::ErrorKind::UnexpectedEof,
                    "unexpected eof while reading chunk body",
                ));
            }
            self.remaining_in_chunk -= read;
            if self.remaining_in_chunk == 0 {
                self.consume_chunk_suffix()?;
            }
            return Ok(read);
        }
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

fn copy_http_body(
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

fn parse_workspace_request_header(header: &[u8]) -> io::Result<ParsedWorkspaceRequest> {
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

    Ok(ParsedWorkspaceRequest {
        method,
        target,
        headers,
        content_length,
        chunked,
    })
}

fn build_workspace_target_url(
    spec: &ServiceWorkspaceProxySpec,
    target: &str,
) -> io::Result<reqwest::Url> {
    if let Ok(parsed) = reqwest::Url::parse(target) {
        let mut url = reqwest::Url::parse(&format!(
            "http://{}:{}/",
            spec.upstream_host, spec.upstream_port
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
        spec.upstream_host, spec.upstream_port, normalized
    ))
    .map_err(|err| io::Error::new(io::ErrorKind::InvalidInput, err.to_string()))
}

fn should_skip_workspace_request_header(name: &str) -> bool {
    matches!(
        name,
        "host"
            | "connection"
            | "proxy-connection"
            | "accept-encoding"
            | "content-length"
            | "transfer-encoding"
            | "expect"
    )
}

fn should_skip_workspace_response_header(name: &str) -> bool {
    matches!(
        name,
        "connection" | "transfer-encoding" | "content-length" | "content-encoding"
    )
}

fn write_workspace_response_line(
    incoming: &mut TcpStream,
    status: reqwest::StatusCode,
) -> io::Result<()> {
    let reason = status.canonical_reason().unwrap_or("OK");
    incoming.write_all(format!("HTTP/1.1 {} {}\r\n", status.as_u16(), reason).as_bytes())
}

fn write_workspace_response_headers(
    incoming: &mut TcpStream,
    headers: &reqwest::header::HeaderMap,
    spec: &ServiceWorkspaceProxySpec,
) -> io::Result<()> {
    for (name, value) in headers {
        let lower = name.as_str().trim().to_ascii_lowercase();
        if should_skip_workspace_response_header(&lower) {
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
                rewrite_absolute_workspace_url(
                    value_str,
                    &spec.upstream_host,
                    spec.upstream_port,
                    &spec.bind_host,
                    spec.listen_port,
                )
            ),
            "refresh" => format!(
                "{}: {}\r\n",
                name.as_str(),
                rewrite_workspace_refresh_header(
                    value_str,
                    &spec.upstream_host,
                    spec.upstream_port,
                    &spec.bind_host,
                    spec.listen_port,
                )
            ),
            "set-cookie" => format!(
                "{}: {}\r\n",
                name.as_str(),
                rewrite_workspace_set_cookie_header(value_str, &spec.upstream_host)
            ),
            _ => format!("{}: {}\r\n", name.as_str(), value_str),
        };
        incoming.write_all(line.as_bytes())?;
    }
    incoming.write_all(b"Connection: close\r\n\r\n")
}

fn write_workspace_proxy_error_response(
    stream: &mut TcpStream,
    status: u16,
    message: &str,
) -> io::Result<()> {
    let body = format!("{message}\n");
    stream.write_all(
        format!(
            "HTTP/1.1 {status} Bad Gateway\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
            body.len(),
            body
        )
        .as_bytes(),
    )?;
    stream.flush()
}

fn rewrite_proxy_request_header(
    header: &[u8],
    local_host: &str,
    local_port: u16,
    upstream_host: &str,
    upstream_port: u16,
    upstream_authority: &str,
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
                    rebuilt.push(format!("Host: {}", upstream_authority));
                }
                "origin" | "referer" => rebuilt.push(format!(
                    "{}: {}",
                    name.trim(),
                    rewrite_workspace_request_absolute_url(
                        value,
                        local_host,
                        local_port,
                        upstream_host,
                        upstream_port,
                    )
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
        rebuilt.push(format!("Host: {}", upstream_authority));
    }
    if !has_connection {
        rebuilt.push("Connection: close".to_string());
    }
    rebuilt.push(String::new());
    rebuilt.push(String::new());

    Ok((rebuilt.join("\r\n").into_bytes(), content_length, chunked))
}

fn rewrite_workspace_request_absolute_url(
    value: &str,
    local_host: &str,
    local_port: u16,
    upstream_host: &str,
    upstream_port: u16,
) -> String {
    let trimmed = value.trim();
    if trimmed.is_empty() {
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
            let _ = url.set_host(Some(upstream_host));
            let _ = url.set_port(Some(upstream_port));
            return url.to_string();
        }
    }
    trimmed.to_string()
}

fn rewrite_absolute_workspace_url(
    value: &str,
    upstream_host: &str,
    upstream_port: u16,
    local_host: &str,
    local_port: u16,
) -> String {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        return trimmed.to_string();
    }
    if let Ok(mut url) = reqwest::Url::parse(trimmed) {
        let parsed_port = url.port_or_known_default();
        if url
            .host_str()
            .map(|host| host.eq_ignore_ascii_case(upstream_host))
            .unwrap_or(false)
            && parsed_port == Some(upstream_port)
        {
            let _ = url.set_scheme("http");
            let _ = url.set_host(Some(local_host));
            let _ = url.set_port(Some(local_port));
            return url.to_string();
        }
    }
    trimmed.to_string()
}

fn rewrite_workspace_refresh_header(
    value: &str,
    upstream_host: &str,
    upstream_port: u16,
    local_host: &str,
    local_port: u16,
) -> String {
    let trimmed = value.trim();
    if let Some((prefix, url_value)) = trimmed.split_once("url=") {
        return format!(
            "{}url={}",
            prefix,
            rewrite_absolute_workspace_url(
                url_value,
                upstream_host,
                upstream_port,
                local_host,
                local_port,
            )
        );
    }
    trimmed.to_string()
}

fn rewrite_workspace_set_cookie_header(value: &str, upstream_host: &str) -> String {
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
                    .eq_ignore_ascii_case(upstream_host)
            {
                continue;
            }
        }
        rebuilt.push(trimmed.to_string());
    }
    rebuilt.join("; ")
}

fn rewrite_proxy_response_header(
    header: &[u8],
    spec: &ServiceWorkspaceProxySpec,
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
                    rewrite_absolute_workspace_url(
                        value,
                        &spec.upstream_host,
                        spec.upstream_port,
                        &spec.bind_host,
                        spec.listen_port,
                    )
                )),
                "refresh" => rebuilt.push(format!(
                    "{}: {}",
                    name.trim(),
                    rewrite_workspace_refresh_header(
                        value,
                        &spec.upstream_host,
                        spec.upstream_port,
                        &spec.bind_host,
                        spec.listen_port,
                    )
                )),
                "set-cookie" => rebuilt.push(format!(
                    "{}: {}",
                    name.trim(),
                    rewrite_workspace_set_cookie_header(value, &spec.upstream_host)
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

fn bridge_workspace_proxy_connection(
    mut incoming: TcpStream,
    spec: ServiceWorkspaceProxySpec,
) -> io::Result<()> {
    let _ = incoming.set_nonblocking(false);
    let trace_id = next_workspace_proxy_trace_id();
    let bridge_result = (|| -> io::Result<()> {
        let (request_header, request_body_buffer) = read_http_header(&mut incoming)?;
        let parsed = parse_workspace_request_header(&request_header)?;
        let request_method = parsed.method.clone();
        let request_target = parsed.target.clone();
        let url = build_workspace_target_url(&spec, &parsed.target)?;
        let host_header = workspace_header_value(&parsed.headers, "host");
        let origin_header = workspace_header_value(&parsed.headers, "origin");
        let referer_header = workspace_header_value(&parsed.headers, "referer");
        let content_type = workspace_header_value(&parsed.headers, "content-type");
        let transfer_encoding = workspace_header_value(&parsed.headers, "transfer-encoding");
        append_proxy_log_line(
            &spec.log_path,
            "workspace-request",
            trace_id,
            &format!(
                "listen=http://{}:{} upstream={} method={} target={} host={} origin={} referer={} content_type={} content_length={:?} transfer_encoding={} chunked={} buffered_body_bytes={}",
                spec.bind_host,
                spec.listen_port,
                url,
                request_method,
                request_target,
                host_header,
                origin_header,
                referer_header,
                content_type,
                parsed.content_length,
                transfer_encoding,
                parsed.chunked,
                request_body_buffer.len()
            ),
        );
        let upstream_authority = if spec.upstream_port == 80 {
            spec.upstream_host.clone()
        } else {
            format!("{}:{}", spec.upstream_host, spec.upstream_port)
        };
        let target_addr = socket_addr(&spec.upstream_host, spec.upstream_port);
        let mut outgoing = TcpStream::connect(&target_addr).map_err(|err| {
            io::Error::other(format!(
                "upstream connect failed for {} {} -> {}: {}",
                request_method, request_target, target_addr, err
            ))
        })?;
        let _ = outgoing.set_nonblocking(false);
        let _ = incoming.set_nodelay(true);
        let _ = outgoing.set_nodelay(true);

        let (rewritten_request_header, content_length, chunked) = rewrite_proxy_request_header(
            &request_header,
            &spec.bind_host,
            spec.listen_port,
            &spec.upstream_host,
            spec.upstream_port,
            &upstream_authority,
        )?;
        outgoing.write_all(&rewritten_request_header)?;
        copy_http_body(
            request_body_buffer,
            &mut incoming,
            &mut outgoing,
            content_length,
            chunked,
        )?;
        outgoing.flush()?;

        let (response_header, response_body_buffer) =
            read_http_header(&mut outgoing).map_err(|err| {
                io::Error::other(format!(
                    "upstream response header failed for {} {}: {}",
                    request_method, request_target, err
                ))
            })?;
        let rewritten_response_header = rewrite_proxy_response_header(&response_header, &spec)?;
        let response_status = String::from_utf8_lossy(&response_header)
            .lines()
            .next()
            .and_then(|line| line.split_whitespace().nth(1))
            .unwrap_or_default()
            .to_string();
        append_proxy_log_line(
            &spec.log_path,
            "workspace-response",
            trace_id,
            &format!(
                "status={} buffered_body_bytes={}",
                response_status,
                response_body_buffer.len()
            ),
        );
        incoming.write_all(&rewritten_response_header)?;
        if !response_body_buffer.is_empty() {
            incoming.write_all(&response_body_buffer)?;
        }
        io::copy(&mut outgoing, &mut incoming).map_err(|err| {
            io::Error::other(format!(
                "upstream response copy failed for {} {}: {}",
                request_method, request_target, err
            ))
        })?;
        let _ = incoming.flush();
        Ok(())
    })();

    if let Err(err) = bridge_result {
        eprintln!("service workspace proxy failed: {err}");
        append_proxy_log_line(
            &spec.log_path,
            "workspace-error",
            trace_id,
            &format!("error={}", err),
        );
        let _ = write_workspace_proxy_error_response(&mut incoming, 502, &err.to_string());
        return Err(err);
    }

    Ok(())
}

fn run_service_workspace_proxy(
    listener: TcpListener,
    shutdown_rx: std::sync::mpsc::Receiver<()>,
    spec: ServiceWorkspaceProxySpec,
) {
    loop {
        match shutdown_rx.try_recv() {
            Ok(_) | Err(std::sync::mpsc::TryRecvError::Disconnected) => break,
            Err(std::sync::mpsc::TryRecvError::Empty) => {}
        }
        match listener.accept() {
            Ok((stream, _)) => {
                let proxy_spec = spec.clone();
                thread::spawn(move || {
                    if let Err(err) = bridge_workspace_proxy_connection(stream, proxy_spec) {
                        eprintln!("service workspace proxy failed: {err}");
                    }
                });
            }
            Err(err) if err.kind() == io::ErrorKind::WouldBlock => {
                thread::sleep(Duration::from_millis(150));
            }
            Err(err) => {
                eprintln!("service workspace proxy accept failed: {err}");
                thread::sleep(Duration::from_millis(250));
            }
        }
    }
}

fn start_service_workspace_proxy(
    mut spec: ServiceWorkspaceProxySpec,
) -> Result<ServiceWorkspaceProxyHandle, String> {
    let bind_addr = socket_addr(&spec.bind_host, 0);
    let listener = TcpListener::bind(&bind_addr)
        .map_err(|err| format!("本地工作台代理监听失败 {bind_addr}: {err}"))?;
    listener
        .set_nonblocking(true)
        .map_err(|err| format!("本地工作台代理初始化失败 {bind_addr}: {err}"))?;
    spec.listen_port = listener.local_addr().map_err(|err| err.to_string())?.port();
    let (shutdown_tx, shutdown_rx) = mpsc::channel();
    let thread_spec = spec.clone();
    let join =
        thread::spawn(move || run_service_workspace_proxy(listener, shutdown_rx, thread_spec));
    Ok(ServiceWorkspaceProxyHandle {
        spec,
        shutdown: shutdown_tx,
        join: Some(join),
    })
}

fn ensure_service_workspace_proxy(
    app: &AppHandle,
    key: &str,
    upstream_url: &reqwest::Url,
) -> Result<String, String> {
    let mut desired = workspace_proxy_spec_from_url(key, upstream_url)?;
    desired.log_path = workspace_proxy_log_path(app)?;
    let manager = app
        .try_state::<ServiceWorkspaceProxyManagerState>()
        .ok_or_else(|| "工作台代理状态不可用".to_string())?;

    let mut handles = manager
        .handles
        .lock()
        .map_err(|_| "工作台代理锁不可用".to_string())?;

    if let Some(existing) = handles.get(key) {
        let mut expected = desired.clone();
        expected.listen_port = existing.spec.listen_port;
        if existing.spec == expected {
            return Ok(format!(
                "http://{}:{}{}",
                existing.spec.bind_host,
                existing.spec.listen_port,
                existing.spec.launch_path_and_query
            ));
        }
    }

    let old = handles.remove(key);
    drop(handles);
    if let Some(handle) = old {
        stop_service_workspace_proxy(handle);
    }

    let handle = start_service_workspace_proxy(desired)?;
    let local_url = format!(
        "http://{}:{}{}",
        handle.spec.bind_host, handle.spec.listen_port, handle.spec.launch_path_and_query
    );
    let mut handles = manager
        .handles
        .lock()
        .map_err(|_| "工作台代理锁不可用".to_string())?;
    handles.insert(key.to_string(), handle);
    Ok(local_url)
}

#[tauri::command]
fn open_service_workspace(app: AppHandle, input: ServiceWorkspaceInput) -> Result<(), String> {
    let key = if input.key.trim().is_empty() {
        "service".to_string()
    } else {
        sanitize_node_token(input.key.trim())
    };
    let upstream_url = reqwest::Url::parse(input.url.trim()).map_err(|err| err.to_string())?;
    let local_url = ensure_service_workspace_proxy(&app, &key, &upstream_url)?;
    let url = reqwest::Url::parse(&local_url).map_err(|err| err.to_string())?;
    let label = format!("service-{}", key);
    if let Some(window) = app.get_webview_window(&label) {
        window
            .navigate(url.clone())
            .map_err(|err| err.to_string())?;
        ensure_window_visible(&window);
        let _ = window.set_focus();
        return Ok(());
    }
    let title = if input.title.trim().is_empty() {
        "驻阡陌服务工作台".to_string()
    } else {
        input.title.trim().to_string()
    };
    let window = WebviewWindowBuilder::new(&app, label, WebviewUrl::External(url))
        .title(&title)
        .inner_size(1280.0, 860.0)
        .min_inner_size(960.0, 680.0)
        .resizable(true)
        .visible(true)
        .build()
        .map_err(|err| err.to_string())?;
    set_window_icon(&window);
    let _ = window.set_focus();
    Ok(())
}

#[tauri::command]
fn probe_service_workspace(
    input: ServiceWorkspaceProbeInput,
) -> Result<ServiceWorkspaceProbeOutput, String> {
    let url = reqwest::Url::parse(input.url.trim()).map_err(|err| err.to_string())?;
    let client = Client::builder()
        .redirect(reqwest::redirect::Policy::limited(5))
        .timeout(Duration::from_secs(8))
        .build()
        .map_err(|err| err.to_string())?;

    let mut request = client
        .get(url)
        .header(reqwest::header::ACCEPT_ENCODING, "identity")
        .header(reqwest::header::CONNECTION, "close");
    if let Some(host_header) = input.host_header {
        let trimmed = host_header.trim();
        if !trimmed.is_empty() {
            request = request.header(reqwest::header::HOST, trimmed);
        }
    }

    let response = request.send().map_err(|err| err.to_string())?;
    Ok(ServiceWorkspaceProbeOutput {
        reachable: true,
        status: Some(response.status().as_u16()),
        final_url: response.url().to_string(),
    })
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

fn user_node_id_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(app
        .path()
        .app_config_dir()
        .map_err(|err| err.to_string())?
        .join("user-node-id.txt"))
}

fn sanitize_node_token(value: &str) -> String {
    let mut out = String::new();
    for ch in value.chars() {
        if ch.is_ascii_alphanumeric() || ch == '-' {
            out.push(ch.to_ascii_lowercase());
        } else if !out.ends_with('-') {
            out.push('-');
        }
    }
    out.trim_matches('-').to_string()
}

fn load_or_create_user_node_id(app: &AppHandle) -> Result<String, String> {
    let path = user_node_id_path(app)?;
    if path.exists() {
        let value = fs::read_to_string(&path).map_err(|err| err.to_string())?;
        let value = value.trim().to_string();
        if !value.is_empty() {
            return Ok(value);
        }
    }
    let machine_id = load_or_create_p2p_machine_id(app)?;
    let suffix = sanitize_node_token(&machine_id);
    let node_id = if suffix.is_empty() {
        format!(
            "user-node-{}",
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map_err(|err| err.to_string())?
                .as_secs()
        )
    } else {
        format!("user-node-{}", suffix)
    };
    fs::write(path, &node_id).map_err(|err| err.to_string())?;
    Ok(node_id)
}

fn normalize_api_base_url(value: Option<&str>) -> String {
    let trimmed = value.unwrap_or_default().trim().trim_end_matches('/');
    if trimmed.is_empty() {
        DEFAULT_API_BASE_URL.to_string()
    } else {
        trimmed.to_string()
    }
}

fn current_user_node_status(state: &UserNodeAgentState) -> Result<UserNodeStatus, String> {
    let launch = state
        .launch
        .lock()
        .map_err(|_| "用户端节点状态锁不可用".to_string())?
        .clone();
    Ok(UserNodeStatus {
        enabled: launch.enabled,
        registered: launch.registered,
        online: launch.online,
        node_id: launch.node_id,
        node_name: launch.node_name,
        api_base_url: launch.api_base_url,
        owner_email: launch.owner_email,
        owner_role: launch.owner_role,
        last_register_at: launch.last_register_at,
        last_heartbeat_at: launch.last_heartbeat_at,
        recommended_heartbeat_sec: launch.recommended_heartbeat_sec,
        p2p_running: launch.p2p_running,
        p2p_virtual_ipv4: launch.p2p_virtual_ipv4,
        p2p_peer_count: launch.p2p_peer_count,
        p2p_hostname: launch.p2p_hostname,
        last_error: launch.last_error,
    })
}

fn update_user_node_identity(
    state: &UserNodeAgentState,
    identity: UserNodeIdentityInput,
) -> Result<(), String> {
    let mut current = state
        .identity
        .lock()
        .map_err(|_| "用户端节点身份锁不可用".to_string())?;
    current.email = identity.email.unwrap_or_default().trim().to_string();
    current.role = identity.role.unwrap_or_default().trim().to_string();
    current.display_name = identity.display_name.unwrap_or_default().trim().to_string();
    Ok(())
}

fn infer_user_node_identity(
    app: &AppHandle,
    state: &UserNodeAgentState,
) -> Result<UserNodeIdentityState, String> {
    let mut identity = state
        .identity
        .lock()
        .map_err(|_| "用户端节点身份锁不可用".to_string())?
        .clone();
    if identity.email.is_empty() {
        if let Ok(profiles) = read_login_profiles_file(app) {
            if let Some(last_used_email) = profiles.last_used_email {
                identity.email = last_used_email.trim().to_string();
            }
        }
    }
    if !identity.email.is_empty() {
        let mut stored = state
            .identity
            .lock()
            .map_err(|_| "用户端节点身份锁不可用".to_string())?;
        if stored.email.is_empty() {
            stored.email = identity.email.clone();
        }
    }
    Ok(identity)
}

fn local_hostname_fallback() -> String {
    std::env::var("COMPUTERNAME")
        .or_else(|_| std::env::var("HOSTNAME"))
        .unwrap_or_else(|_| "cloud-relay-user".to_string())
        .trim()
        .to_string()
}

fn user_node_name_from_status(status: &P2PRuntimeStatus) -> String {
    let hostname = if !status.node_hostname.trim().is_empty() {
        status.node_hostname.trim().to_string()
    } else {
        local_hostname_fallback()
    };
    format!("驻阡陌用户端-{}", hostname)
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

fn build_user_node_register_request(
    node_id: String,
    node_name: String,
    owner: &UserNodeIdentityState,
    api_base_url: &str,
    p2p_status: &P2PRuntimeStatus,
) -> AgentNodeRegisterRequest {
    let mut metadata = HashMap::new();
    metadata.insert("hostname".to_string(), local_hostname_fallback());
    metadata.insert("os".to_string(), std::env::consts::OS.to_string());
    metadata.insert("arch".to_string(), std::env::consts::ARCH.to_string());
    metadata.insert("deploymentMode".to_string(), "user_console".to_string());
    metadata.insert("nodeRole".to_string(), "local".to_string());
    metadata.insert("environment".to_string(), "prod".to_string());
    metadata.insert("trustLevel".to_string(), "trusted".to_string());
    metadata.insert("appSurface".to_string(), "user-console".to_string());
    metadata.insert("apiBaseUrl".to_string(), api_base_url.to_string());
    metadata.insert("p2pRuntime".to_string(), "easytier".to_string());
    metadata.insert("p2pRpcPortal".to_string(), USER_P2P_RPC_PORTAL.to_string());
    metadata.insert(
        "p2pRunning".to_string(),
        if p2p_status.running { "true" } else { "false" }.to_string(),
    );
    if !p2p_status.virtual_ipv4.trim().is_empty() {
        metadata.insert(
            "p2pVirtualIpv4".to_string(),
            p2p_status.virtual_ipv4.trim().to_string(),
        );
    }
    if !p2p_status.node_hostname.trim().is_empty() {
        metadata.insert(
            "p2pHostname".to_string(),
            p2p_status.node_hostname.trim().to_string(),
        );
    }
    if !owner.email.is_empty() {
        metadata.insert("owner".to_string(), owner.email.clone());
    }
    if !owner.role.is_empty() {
        metadata.insert("ownerRole".to_string(), owner.role.clone());
    }
    if !owner.display_name.is_empty() {
        metadata.insert("ownerDisplayName".to_string(), owner.display_name.clone());
    }

    AgentNodeRegisterRequest {
        node_id,
        node_name,
        agent_version: env!("CARGO_PKG_VERSION").to_string(),
        capabilities: AgentNodeCapabilities {
            tcp_relay: false,
            http_relay: false,
            https_relay: false,
            udp_relay: false,
            p2p_assist: true,
            socks5_connect: false,
        },
        metadata,
    }
}

fn build_user_node_metrics(
    owner: &UserNodeIdentityState,
    p2p_status: &P2PRuntimeStatus,
) -> HashMap<String, String> {
    let mut metrics = HashMap::new();
    metrics.insert("pid".to_string(), std::process::id().to_string());
    metrics.insert("p2p:enabled".to_string(), "true".to_string());
    metrics.insert(
        "p2p:running".to_string(),
        if p2p_status.running { "true" } else { "false" }.to_string(),
    );
    metrics.insert("p2p:runtime".to_string(), "easytier".to_string());
    metrics.insert(
        "p2p:rpc_portal".to_string(),
        USER_P2P_RPC_PORTAL.to_string(),
    );
    metrics.insert(
        "p2p:peer_count".to_string(),
        p2p_status.peer_count.to_string(),
    );
    if !p2p_status.node_hostname.trim().is_empty() {
        metrics.insert(
            "p2p:hostname".to_string(),
            p2p_status.node_hostname.trim().to_string(),
        );
    }
    if !p2p_status.virtual_ipv4.trim().is_empty() {
        metrics.insert(
            "p2p:ipv4".to_string(),
            p2p_status.virtual_ipv4.trim().to_string(),
        );
    }
    if !p2p_status.instance_id.trim().is_empty() {
        metrics.insert(
            "p2p:instance_id".to_string(),
            p2p_status.instance_id.trim().to_string(),
        );
    }
    if !owner.email.is_empty() {
        metrics.insert("user:email".to_string(), owner.email.clone());
    }
    if !owner.role.is_empty() {
        metrics.insert("user:role".to_string(), owner.role.clone());
    }
    metrics
}

fn user_node_http_client() -> Result<Client, String> {
    Client::builder()
        .timeout(Duration::from_secs(20))
        .build()
        .map_err(|err| err.to_string())
}

fn register_user_node(
    client: &Client,
    api_base_url: &str,
    payload: &AgentNodeRegisterRequest,
) -> Result<AgentNodeRegisterResponse, String> {
    let response = client
        .post(format!("{}/agent/register", api_base_url))
        .json(payload)
        .send()
        .map_err(|err| format!("注册后台节点失败: {err}"))?;
    let status = response.status();
    let body = response
        .text()
        .map_err(|err| format!("读取后台节点注册响应失败: {err}"))?;
    if !status.is_success() {
        return Err(format!(
            "注册后台节点失败: http {} {}",
            status.as_u16(),
            body
        ));
    }
    serde_json::from_str(&body).map_err(|err| format!("解析后台节点注册响应失败: {err}"))
}

fn heartbeat_user_node(
    client: &Client,
    api_base_url: &str,
    payload: &AgentNodeHeartbeatRequest,
) -> Result<(), String> {
    let response = client
        .post(format!("{}/agent/heartbeat", api_base_url))
        .json(payload)
        .send()
        .map_err(|err| format!("发送后台节点心跳失败: {err}"))?;
    let status = response.status();
    let body = response
        .text()
        .map_err(|err| format!("读取后台节点心跳响应失败: {err}"))?;
    if !status.is_success() {
        return Err(format!(
            "发送后台节点心跳失败: http {} {}",
            status.as_u16(),
            body
        ));
    }
    Ok(())
}

fn sync_user_node_registration(
    app: &AppHandle,
    runtime: &P2PRuntimeManagerState,
    state: &UserNodeAgentState,
) -> Result<UserNodeStatus, String> {
    let _guard = state
        .op
        .lock()
        .map_err(|_| "用户端节点同步锁不可用".to_string())?;

    ensure_config_dir(app)?;
    let config = load_app_config(app.clone()).unwrap_or_default();
    let api_base_url = normalize_api_base_url(config.api_base_url.as_deref());
    let p2p_status = current_p2p_runtime_status(app, runtime).unwrap_or(P2PRuntimeStatus {
        available: false,
        configured: false,
        running: false,
        pid: None,
        started_at: None,
        executable_path: String::new(),
        work_dir: String::new(),
        stdout_log_path: String::new(),
        stderr_log_path: String::new(),
        args_summary: String::new(),
        last_error: String::new(),
        machine_id: String::new(),
        rpc_portal: USER_P2P_RPC_PORTAL.to_string(),
        node_hostname: String::new(),
        virtual_ipv4: String::new(),
        instance_id: String::new(),
        peer_count: 0,
        connected_peers: Vec::new(),
    });
    let node_id = load_or_create_user_node_id(app)?;
    let node_name = user_node_name_from_status(&p2p_status);

    let sync_result = (|| -> Result<UserNodeStatus, String> {
        let identity = infer_user_node_identity(app, state)?;
        let client = user_node_http_client()?;
        let register_payload = build_user_node_register_request(
            node_id.clone(),
            node_name.clone(),
            &identity,
            &api_base_url,
            &p2p_status,
        );
        let register_out = register_user_node(&client, &api_base_url, &register_payload)?;
        let recommended = register_out
            .recommended_heartbeat_sec
            .unwrap_or(USER_NODE_HEARTBEAT_SEC);
        let heartbeat_payload = AgentNodeHeartbeatRequest {
            node_id: register_out.node_id.clone(),
            metrics: build_user_node_metrics(&identity, &p2p_status),
            observed_at: chrono_like_now_rfc3339(),
            active_tunnels: 0,
        };
        heartbeat_user_node(&client, &api_base_url, &heartbeat_payload)?;

        let now = now_millis();
        let mut launch = state
            .launch
            .lock()
            .map_err(|_| "用户端节点状态锁不可用".to_string())?;
        launch.enabled = true;
        launch.registered = true;
        launch.online = true;
        launch.node_id = register_out.node_id;
        launch.node_name = node_name.clone();
        launch.api_base_url = api_base_url.clone();
        launch.owner_email = identity.email;
        launch.owner_role = identity.role;
        launch.last_register_at = Some(now);
        launch.last_heartbeat_at = Some(now);
        launch.recommended_heartbeat_sec = recommended;
        launch.p2p_running = p2p_status.running;
        launch.p2p_virtual_ipv4 = p2p_status.virtual_ipv4.clone();
        launch.p2p_peer_count = p2p_status.peer_count;
        launch.p2p_hostname = p2p_status.node_hostname.clone();
        launch.last_error.clear();
        drop(launch);

        current_user_node_status(state)
    })();

    if let Err(err) = &sync_result {
        if let Ok(mut launch) = state.launch.lock() {
            launch.enabled = true;
            launch.online = false;
            launch.node_id = node_id;
            launch.node_name = node_name;
            launch.api_base_url = api_base_url;
            launch.p2p_running = p2p_status.running;
            launch.p2p_virtual_ipv4 = p2p_status.virtual_ipv4;
            launch.p2p_peer_count = p2p_status.peer_count;
            launch.p2p_hostname = p2p_status.node_hostname;
            launch.last_error = err.clone();
        }
    }

    sync_result
}

fn chrono_like_now_rfc3339() -> String {
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs();
    format!("{}", time_like_rfc3339(now))
}

fn time_like_rfc3339(now_secs: u64) -> String {
    use std::time::Duration as StdDuration;
    let timestamp = UNIX_EPOCH + StdDuration::from_secs(now_secs);
    let datetime: chrono::DateTime<chrono::Utc> = timestamp.into();
    datetime.to_rfc3339()
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

fn start_background_bootstrap_tasks(app: &AppHandle) {
    let app_handle = app.clone();
    thread::spawn(move || {
        thread::sleep(Duration::from_millis(80));
        maybe_auto_start_p2p(&app_handle);
        if let (Some(runtime), Some(node_state)) = (
            app_handle.try_state::<P2PRuntimeManagerState>(),
            app_handle.try_state::<UserNodeAgentState>(),
        ) {
            let _ = sync_user_node_registration(&app_handle, &runtime, &node_state);
        }
    });
}

fn start_user_node_agent_loop(app: &AppHandle) {
    let app_handle = app.clone();
    thread::spawn(move || loop {
        thread::sleep(Duration::from_secs(USER_NODE_HEARTBEAT_SEC));
        let Some(runtime) = app_handle.try_state::<P2PRuntimeManagerState>() else {
            continue;
        };
        let Some(node_state) = app_handle.try_state::<UserNodeAgentState>() else {
            continue;
        };
        let _ = sync_user_node_registration(&app_handle, &runtime, &node_state);
    });
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
    stop_all_service_workspace_proxies(app);
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
                stop_all_service_workspace_proxies(app);
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
        .manage(UserNodeAgentState::default())
        .manage(ServiceWorkspaceProxyManagerState::default())
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
            start_background_bootstrap_tasks(app.handle());
            start_user_node_agent_loop(app.handle());
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
                        stop_all_service_workspace_proxies(&window.app_handle());
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
            user_node_status,
            user_node_sync,
            open_p2p_runtime_log,
            window_start_drag,
            window_minimize,
            window_toggle_maximize,
            window_request_close,
            app_exit,
            open_external,
            open_service_workspace,
            open_service_workspace_external,
            probe_service_workspace,
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
            stop_all_service_workspace_proxies(&app_handle);
        }
    });
}
