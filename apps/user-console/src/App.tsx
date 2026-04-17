import { useCallback, useEffect, useMemo, useState } from "react";
import type { MouseEvent } from "react";
import { listen } from "@tauri-apps/api/event";
import { createDesktopApi } from "../../../packages/desktop-core/src/api";
import type { UserSummary } from "../../../packages/desktop-core/src/types";
import {
  appExit,
  createTauriDesktopTransport,
  decryptLoginPassword,
  deleteLoginProfile,
  loadAppConfig,
  loadP2PRuntimeStatus,
  loadDesktopHostPaths,
  openP2PRuntimeLog,
  readLoginProfiles,
  saveAppConfig,
  saveLoginProfile,
  startP2PRuntime,
  stopP2PRuntime,
  windowMinimize,
  windowRequestClose,
  windowStartDrag,
  windowToggleMaximize,
  type AppConfig,
  type LoginProfilesFile,
  type P2PRuntimeStatus,
} from "./desktopHost";

type CloseAction = "ask" | "tray" | "exit";
type DriveFallbackPolicy = "admin_only" | "never";
type ImageBulkUploadMode = "p2p_bulk_https_light" | "https_only";
type Page = "loading" | "login" | "home" | "drive" | "gallery" | "p2p" | "settings";
type ReopenDialogChoice = "cancel" | "new-window";

const defaultApiUrl = "https://manage.020309.top";
const defaultConfig: Required<Pick<AppConfig, "closeAction" | "driveFallbackPolicy" | "imageBulkUploadMode" | "p2pUseDhcp">> = {
  closeAction: "ask",
  driveFallbackPolicy: "admin_only",
  imageBulkUploadMode: "p2p_bulk_https_light",
  p2pUseDhcp: true,
};

function normalizeApiBaseUrl(value?: string) {
  return (value || "").trim().replace(/\/+$/, "") || defaultApiUrl;
}

function optionalTrimmed(value?: string) {
  return (value || "").trim();
}

function mergeConfig(config?: AppConfig | null): AppConfig {
  return {
    apiBaseUrl: normalizeApiBaseUrl(config?.apiBaseUrl),
    closeAction: (config?.closeAction || defaultConfig.closeAction) as CloseAction,
    p2pAutoStart: Boolean(config?.p2pAutoStart),
    driveFallbackPolicy: (config?.driveFallbackPolicy || defaultConfig.driveFallbackPolicy) as DriveFallbackPolicy,
    imageBulkUploadMode: (config?.imageBulkUploadMode || defaultConfig.imageBulkUploadMode) as ImageBulkUploadMode,
    p2pNetworkName: optionalTrimmed(config?.p2pNetworkName),
    p2pNetworkSecret: optionalTrimmed(config?.p2pNetworkSecret),
    p2pPeerUrl: config?.p2pPeerUrl || "",
    p2pVirtualIpv4: optionalTrimmed(config?.p2pVirtualIpv4),
    p2pUseDhcp: config?.p2pUseDhcp !== false,
    p2pInstanceName: optionalTrimmed(config?.p2pInstanceName),
    p2pHostname: optionalTrimmed(config?.p2pHostname),
  };
}

function formatStartedAt(timestamp?: number | null) {
  if (!timestamp) return "未启动";
  return new Date(timestamp).toLocaleString();
}

function p2pRuntimeLabel(status?: P2PRuntimeStatus | null) {
  if (!status) return "读取中";
  if (status.running) return "运行中";
  if (!status.available) return "未打包 easytier-core";
  if (!status.configured) return "待配置";
  return "已停止";
}

function roleLabel(role?: string) {
  switch (role) {
    case "admin":
      return "管理员";
    case "manager":
      return "管理用户";
    case "user":
      return "普通用户";
    default:
      return "未登录";
  }
}

export default function App() {
  const [page, setPage] = useState<Page>("loading");
  const [user, setUser] = useState<UserSummary | null>(null);
  const [initializing, setInitializing] = useState(true);
  const [busy, setBusy] = useState(false);
  const [p2pBusy, setP2PBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [closeDialogOpen, setCloseDialogOpen] = useState(false);
  const [reopenDialogOpen, setReopenDialogOpen] = useState(false);
  const [deleteConfirmTarget, setDeleteConfirmTarget] = useState<string | null>(null);
  const [loginProfiles, setLoginProfiles] = useState<LoginProfilesFile | null>(null);
  const [profileListOpen, setProfileListOpen] = useState(false);
  const [appConfig, setAppConfig] = useState<AppConfig>(mergeConfig(null));
  const [desktopHostPaths, setDesktopHostPaths] = useState({
    configDir: "未接入 Tauri 宿主",
    logDir: "未接入 Tauri 宿主",
    available: false,
  });
  const [p2pStatus, setP2PStatus] = useState<P2PRuntimeStatus | null>(null);

  const [apiUrl, setApiUrl] = useState(defaultApiUrl);
  const [loginEmail, setLoginEmail] = useState("");
  const [loginPassword, setLoginPassword] = useState("");
  const [savePassword, setSavePassword] = useState(false);
  const [autoLogin, setAutoLogin] = useState(false);

  const transport = useMemo(() => createTauriDesktopTransport(), []);
  const api = useMemo(() => createDesktopApi(apiUrl, transport || undefined), [apiUrl, transport]);

  const refreshLoginProfiles = useCallback(async () => {
    const profiles = await readLoginProfiles();
    setLoginProfiles(profiles);
    return profiles;
  }, []);

  const persistConfig = useCallback(async (nextConfig: AppConfig) => {
    const merged = mergeConfig(nextConfig);
    setAppConfig(merged);
    setApiUrl(merged.apiBaseUrl || defaultApiUrl);
    await saveAppConfig(merged);
  }, []);

  const refreshP2PStatus = useCallback(async () => {
    const status = await loadP2PRuntimeStatus();
    setP2PStatus(status);
    return status;
  }, []);

  const fillProfileCredentials = useCallback(async (email: string, profiles?: LoginProfilesFile | null) => {
    const activeProfiles = profiles ?? loginProfiles;
    const profile = activeProfiles?.profiles.find((item) => item.email === email);
    setLoginEmail(email);
    setProfileListOpen(false);
    if (!profile) {
      setLoginPassword("");
      setSavePassword(false);
      setAutoLogin(false);
      return;
    }
    try {
      const password = await decryptLoginPassword(email);
      setLoginPassword(password);
      setSavePassword(Boolean(password));
      setAutoLogin(Boolean(password) && profile.autoLogin);
    } catch {
      setLoginPassword("");
      setSavePassword(false);
      setAutoLogin(false);
    }
  }, [loginProfiles]);

  const loadCurrentSession = useCallback(async () => {
    try {
      const response = await api.loadCurrentUser();
      setUser(response.user);
      setPage("home");
      setError("");
      return true;
    } catch {
      setUser(null);
      return false;
    }
  }, [api]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [config, profiles, hostPaths, runtimeStatus] = await Promise.all([
          loadAppConfig(),
          readLoginProfiles(),
          loadDesktopHostPaths(),
          loadP2PRuntimeStatus(),
        ]);
        if (cancelled) return;

        const mergedConfig = mergeConfig(config);
        setAppConfig(mergedConfig);
        setApiUrl(mergedConfig.apiBaseUrl || defaultApiUrl);
        setLoginProfiles(profiles);
        setDesktopHostPaths(hostPaths);
        setP2PStatus(runtimeStatus);

        const lastEmail = profiles?.lastUsedEmail || profiles?.profiles[0]?.email || "";
        if (lastEmail) {
          setLoginEmail(lastEmail);
        }
        const lastProfile = lastEmail ? profiles?.profiles.find((item) => item.email === lastEmail) : null;
        let rememberedPassword = "";
        if (lastProfile) {
          try {
            rememberedPassword = await decryptLoginPassword(lastEmail);
          } catch {
            rememberedPassword = "";
          }
          if (cancelled) return;
          setLoginPassword(rememberedPassword);
          setSavePassword(Boolean(rememberedPassword));
          setAutoLogin(Boolean(rememberedPassword) && lastProfile.autoLogin);
        }

        const authed = await createDesktopApi(mergedConfig.apiBaseUrl || defaultApiUrl, transport || undefined).loadCurrentUser()
          .then((response) => {
            if (cancelled) return true;
            setUser(response.user);
            setPage("home");
            return true;
          })
          .catch(() => false);

        if (!cancelled && !authed) {
          if (lastProfile?.autoLogin && rememberedPassword) {
            try {
              const response = await createDesktopApi(mergedConfig.apiBaseUrl || defaultApiUrl, transport || undefined).login(lastEmail, rememberedPassword);
              if (!cancelled) {
                setUser(response.user);
                setPage("home");
              }
            } catch {
              if (!cancelled) {
                setPage("login");
              }
            }
          } else {
            setPage("login");
          }
        }
      } finally {
        if (!cancelled) {
          setInitializing(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [transport]);

  useEffect(() => {
    if (!("__TAURI_INTERNALS__" in window)) return;
    let closeDispose: undefined | (() => void);
    let relaunchDispose: undefined | (() => void);
    void listen("app-close-requested", () => {
      setCloseDialogOpen(true);
    }).then((dispose) => {
      closeDispose = dispose;
    }).catch(() => {
      closeDispose = undefined;
    });
    void listen("second-launch-requested", () => {
      setReopenDialogOpen(true);
    }).then((dispose) => {
      relaunchDispose = dispose;
    }).catch(() => {
      relaunchDispose = undefined;
    });
    return () => {
      closeDispose?.();
      relaunchDispose?.();
    };
  }, []);

  const saveAndReconnect = useCallback(async () => {
    setBusy(true);
    setError("");
    try {
      await persistConfig({ ...appConfig, apiBaseUrl: apiUrl });
      const ok = await loadCurrentSession();
      if (!ok) {
        setPage("login");
      }
      setNotice("配置已保存。");
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "保存失败");
    } finally {
      setBusy(false);
    }
  }, [apiUrl, appConfig, loadCurrentSession, persistConfig]);

  const handleLogin = useCallback(async () => {
    if (!loginEmail.trim() || !loginPassword.trim()) {
      setError("请输入邮箱和密码");
      return;
    }
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await persistConfig({ ...appConfig, apiBaseUrl: apiUrl });
      const response = await createDesktopApi(normalizeApiBaseUrl(apiUrl), transport || undefined).login(loginEmail.trim(), loginPassword);
      setUser(response.user);
      setPage("home");
      if (savePassword) {
        await saveLoginProfile(loginEmail.trim(), loginPassword, autoLogin);
        await refreshLoginProfiles();
      }
      setNotice("登录成功。");
    } catch (loginError) {
      setError(loginError instanceof Error ? loginError.message : "登录失败");
    } finally {
      setBusy(false);
    }
  }, [apiUrl, appConfig, autoLogin, loginEmail, loginPassword, persistConfig, refreshLoginProfiles, savePassword, transport]);

  const handleLogout = useCallback(async () => {
    setBusy(true);
    try {
      await api.logout();
    } catch {
      // ignore logout transport errors
    } finally {
      setUser(null);
      setPage("login");
      setBusy(false);
    }
  }, [api]);

  const handleDeleteProfile = useCallback(async (email: string) => {
    try {
      await deleteLoginProfile(email);
      const updated = await refreshLoginProfiles();
      if (loginEmail === email) {
        const nextEmail = updated?.lastUsedEmail || updated?.profiles[0]?.email || "";
        if (nextEmail) {
          await fillProfileCredentials(nextEmail, updated);
        } else {
          setLoginEmail("");
          setLoginPassword("");
          setSavePassword(false);
          setAutoLogin(false);
        }
      }
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : "删除失败");
    }
  }, [fillProfileCredentials, loginEmail, refreshLoginProfiles]);

  const handleWindowClose = useCallback(() => {
    const closeAction = (appConfig.closeAction || defaultConfig.closeAction) as CloseAction;
    if (closeAction === "ask") {
      setCloseDialogOpen(true);
      return;
    }
    if (closeAction === "tray") {
      void windowRequestClose();
      return;
    }
    void appExit();
  }, [appConfig.closeAction]);

  const handleCloseDialogChoice = useCallback(async (action: "tray" | "exit", dontAskAgain: boolean) => {
    setCloseDialogOpen(false);
    if (dontAskAgain) {
      const nextConfig = { ...appConfig, closeAction: action };
      setAppConfig(nextConfig);
      try {
        await saveAppConfig(nextConfig);
      } catch {
        // ignore
      }
    }
    if (action === "tray") {
      void windowRequestClose();
      return;
    }
    void appExit();
  }, [appConfig]);

  const handleReopenChoice = useCallback((_choice: ReopenDialogChoice) => {
    setReopenDialogOpen(false);
  }, []);

  const handleTitlebarDrag = useCallback((event: MouseEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    const target = event.target as HTMLElement | null;
    if (target?.closest("button, input, select, textarea, a, label")) return;
    void windowStartDrag();
  }, []);

  useEffect(() => {
    if (initializing || page !== "p2p") return;
    void refreshP2PStatus();
    const timer = window.setInterval(() => {
      void refreshP2PStatus();
    }, 5000);
    return () => {
      window.clearInterval(timer);
    };
  }, [initializing, page, refreshP2PStatus]);

  const saveP2PSettings = useCallback(async () => {
    setP2PBusy(true);
    setError("");
    try {
      const merged = mergeConfig(appConfig);
      setAppConfig(merged);
      await saveAppConfig(merged);
      await refreshP2PStatus();
      setNotice("P2P 设置已保存。若 EasyTier 已在运行，请停止后重新启动以应用新参数。");
    } catch (configError) {
      setError(configError instanceof Error ? configError.message : "保存 P2P 设置失败");
    } finally {
      setP2PBusy(false);
    }
  }, [appConfig, refreshP2PStatus]);

  const handleP2PRuntimeStart = useCallback(async () => {
    setP2PBusy(true);
    setError("");
    setNotice("");
    try {
      const merged = mergeConfig(appConfig);
      setAppConfig(merged);
      await saveAppConfig(merged);
      const status = await startP2PRuntime();
      setP2PStatus(status);
      setNotice("EasyTier 已启动。");
    } catch (startError) {
      setError(startError instanceof Error ? startError.message : "启动 EasyTier 失败");
    } finally {
      setP2PBusy(false);
    }
  }, [appConfig]);

  const handleP2PRuntimeStop = useCallback(async () => {
    setP2PBusy(true);
    setError("");
    setNotice("");
    try {
      const status = await stopP2PRuntime();
      setP2PStatus(status);
      setNotice("EasyTier 已停止。");
    } catch (stopError) {
      setError(stopError instanceof Error ? stopError.message : "停止 EasyTier 失败");
    } finally {
      setP2PBusy(false);
    }
  }, []);

  const handleOpenP2PLog = useCallback(async (kind: "stdout" | "stderr") => {
    try {
      await openP2PRuntimeLog(kind);
    } catch (logError) {
      setError(logError instanceof Error ? logError.message : "打开日志失败");
    }
  }, []);

  const renderLoginPage = () => (
    <div className="card auth-card">
      <h2>登录用户端</h2>
      <p className="helper-text">用户端用于访问网盘、执行批量图床上传、接入 P2P 网络。云端仍负责鉴权、目录和公开入口。</p>
      <label>云端地址</label>
      <input value={apiUrl} onChange={(event) => setApiUrl(event.target.value)} placeholder={defaultApiUrl} />
      <label>邮箱</label>
      {loginProfiles?.profiles.length ? (
        <div className="profile-selector">
          <div className="profile-input-wrap">
            <input value={loginEmail} onChange={(event) => setLoginEmail(event.target.value)} onFocus={() => setProfileListOpen(false)} />
            <button type="button" className="profile-arrow-btn" onClick={() => setProfileListOpen((current) => !current)}>
              {profileListOpen ? "▴" : "▾"}
            </button>
          </div>
          {profileListOpen ? (
            <div className="profile-dropdown">
              {loginProfiles.profiles.map((profile) => (
                <div
                  key={profile.email}
                  className={`profile-row${loginEmail === profile.email ? " selected" : ""}`}
                  onClick={() => void fillProfileCredentials(profile.email)}
                >
                  <span className="profile-email">{profile.email}{profile.autoLogin ? " (自动登录)" : ""}</span>
                  <button
                    type="button"
                    className="profile-delete-btn"
                    onClick={(event) => {
                      event.stopPropagation();
                      setDeleteConfirmTarget(profile.email);
                    }}
                  >
                    删除
                  </button>
                </div>
              ))}
            </div>
          ) : null}
        </div>
      ) : (
        <input value={loginEmail} onChange={(event) => setLoginEmail(event.target.value)} />
      )}
      <label>密码</label>
      <input type="password" value={loginPassword} onChange={(event) => setLoginPassword(event.target.value)} />
      <label className="check-row">
        <input
          type="checkbox"
          checked={savePassword}
          onChange={(event) => {
            if (autoLogin && !event.target.checked) return;
            setSavePassword(event.target.checked);
          }}
          disabled={autoLogin}
        />
        <span>保存密码{autoLogin ? "（自动登录需要）" : ""}</span>
      </label>
      <label className="check-row">
        <input
          type="checkbox"
          checked={autoLogin}
          onChange={(event) => {
            const next = event.target.checked;
            setAutoLogin(next);
            if (next) {
              setSavePassword(true);
            }
          }}
        />
        <span>自动登录</span>
      </label>
      <div className="action-row">
        <button className="primary" type="button" onClick={() => void handleLogin()} disabled={busy}>
          {busy ? "登录中..." : "登录"}
        </button>
        <button className="secondary" type="button" onClick={() => void saveAndReconnect()} disabled={busy}>
          保存地址
        </button>
      </div>
    </div>
  );

  const driveFallbackLabel = (appConfig.driveFallbackPolicy || defaultConfig.driveFallbackPolicy) === "admin_only"
    ? "仅管理员允许云端回退下载"
    : "禁止云端回退，必须直连 P2P";
  const imageUploadLabel = (appConfig.imageBulkUploadMode || defaultConfig.imageBulkUploadMode) === "p2p_bulk_https_light"
    ? "批量走 P2P，轻量走 HTTPS"
    : "全部走 HTTPS";

  return (
    <div className="app-shell">
      <div className="titlebar" onMouseDown={handleTitlebarDrag}>
        <span className="titlebar-text">驻阡陌用户端</span>
        <div className="titlebar-actions">
          <button className="tb-btn" type="button" onClick={() => void windowMinimize()}>─</button>
          <button className="tb-btn" type="button" onClick={() => void windowToggleMaximize()}>□</button>
          <button className="tb-btn close" type="button" onClick={handleWindowClose}>×</button>
        </div>
      </div>

      <div className="sidebar">
        <div className="sidebar-brand">驻阡陌用户端</div>
        {user ? (
          <>
            <button className={`nav-btn ${page === "home" ? "active" : ""}`} type="button" onClick={() => setPage("home")}>总览</button>
            <button className={`nav-btn ${page === "drive" ? "active" : ""}`} type="button" onClick={() => setPage("drive")}>网盘</button>
            <button className={`nav-btn ${page === "gallery" ? "active" : ""}`} type="button" onClick={() => setPage("gallery")}>图床</button>
            <button className={`nav-btn ${page === "p2p" ? "active" : ""}`} type="button" onClick={() => setPage("p2p")}>P2P 网络</button>
            <button className={`nav-btn ${page === "settings" ? "active" : ""}`} type="button" onClick={() => setPage("settings")}>设置</button>
            <div className="sidebar-footer">
              <div className="user-info">
                <div>{user.email}</div>
                <div className="user-role">{roleLabel(user.role)}</div>
              </div>
              <button className="logout-btn" type="button" onClick={() => void handleLogout()}>退出登录</button>
            </div>
          </>
        ) : (
          <div className="sidebar-footer">
            <div className="user-info">请先登录云端</div>
          </div>
        )}
      </div>

      <main className="main">
        {error ? <div className="error-bar" onClick={() => setError("")}>{error}</div> : null}
        {notice ? <div className="success-bar" onClick={() => setNotice("")}>{notice}</div> : null}

        {initializing || page === "loading" ? (
          <div className="card">
            <h2>初始化中</h2>
            <p className="helper-text">正在读取本地配置、登录状态和用户端工作目录。</p>
          </div>
        ) : null}

        {!initializing && page === "login" ? renderLoginPage() : null}

        {!initializing && user && page === "home" ? (
          <>
            <div className="page-header">
              <h1>用户端总览</h1>
              <p>区分三端：云端负责控制面与公网入口，服务端承载网盘/图床，用户端负责访问与 P2P 直连。</p>
            </div>
            <div className="grid two">
              <section className="card">
                <h2>角色边界</h2>
                <div className="info-list">
                  <div><strong>云端</strong><span>负责登录、授权、公开域名、轻量入口与回退策略。</span></div>
                  <div><strong>服务端</strong><span>部署网盘、图床等真实业务服务，并加入 EasyTier。</span></div>
                  <div><strong>用户端</strong><span>安装独立壳，加入 EasyTier，负责 P2P 下载和批量上传。</span></div>
                </div>
              </section>
              <section className="card">
                <h2>当前策略</h2>
                <div className="policy-chip">网盘: {driveFallbackLabel}</div>
                <div className="policy-chip">图床上传: {imageUploadLabel}</div>
                <div className="policy-chip">图床读取: 保持云端 HTTPS 公网入口</div>
                <div className="policy-chip">P2P 随应用启动: {appConfig.p2pAutoStart ? "已开启" : "未开启"}</div>
                <div className="policy-chip">EasyTier 运行态: {p2pRuntimeLabel(p2pStatus)}</div>
              </section>
            </div>
          </>
        ) : null}

        {!initializing && user && page === "drive" ? (
          <>
            <div className="page-header">
              <h1>网盘访问</h1>
              <p>用户端面向大文件下载。目标是 P2P 直连优先，不默认回退到云端中继。</p>
            </div>
            <div className="grid two">
              <section className="card">
                <h2>访问策略</h2>
                <ul className="plain-list">
                  <li>普通用户没有 EasyTier 端时，不提供云端大文件下载。</li>
                  <li>管理员可保留受控云端回退，用于紧急维护和核对。</li>
                  <li>目录、分享页、鉴权信息继续由云端提供。</li>
                </ul>
              </section>
              <section className="card">
                <h2>接入状态</h2>
                <div className="state-row"><span>用户角色</span><span>{roleLabel(user.role)}</span></div>
                <div className="state-row"><span>云端回退</span><span>{driveFallbackLabel}</span></div>
                <div className="state-row"><span>P2P 客户端</span><span>{p2pRuntimeLabel(p2pStatus)}</span></div>
                <div className="state-row"><span>后续能力</span><span>下载任务、断点续传、本地缓存</span></div>
              </section>
            </div>
          </>
        ) : null}

        {!initializing && user && page === "gallery" ? (
          <>
            <div className="page-header">
              <h1>图床</h1>
              <p>图床拆成两条路径：轻量日常操作保留 HTTPS，批量上传通过专门的 P2P 入口走服务端。</p>
            </div>
            <div className="grid two">
              <section className="card">
                <h2>批量上传</h2>
                <ul className="plain-list">
                  <li>新增单独的 P2P 上传入口，不复用公开读图入口。</li>
                  <li>用户端在大量传图时直接对服务端发起 P2P 上传。</li>
                  <li>这样可以把高流量上传从云端摘掉。</li>
                </ul>
              </section>
              <section className="card">
                <h2>日常读取</h2>
                <ul className="plain-list">
                  <li>博客图片、日常浏览、外链展示继续使用云端 HTTPS。</li>
                  <li>公开访问保持现有域名与缓存策略，不要求访客安装用户端。</li>
                  <li>读取链路和上传链路明确分开，避免同一入口混杂策略。</li>
                </ul>
              </section>
            </div>
          </>
        ) : null}

        {!initializing && user && page === "p2p" ? (
          <>
            <div className="page-header">
              <h1>P2P 网络</h1>
              <p>P2P 是节点级覆盖网络，不是单条隧道。这里单独管理 EasyTier 生命周期和用户端策略。</p>
            </div>
            <div className="grid two">
              <section className="card">
                <h2>本地策略</h2>
                <label className="check-row">
                  <input
                    type="checkbox"
                    checked={Boolean(appConfig.p2pAutoStart)}
                    onChange={(event) => setAppConfig((current) => ({ ...current, p2pAutoStart: event.target.checked }))}
                  />
                  <span>随用户端启动 EasyTier</span>
                </label>
                <label>网盘回退策略</label>
                <select
                  value={appConfig.driveFallbackPolicy || defaultConfig.driveFallbackPolicy}
                  onChange={(event) => setAppConfig((current) => ({ ...current, driveFallbackPolicy: event.target.value as DriveFallbackPolicy }))}
                >
                  <option value="admin_only">仅管理员允许云端回退</option>
                  <option value="never">禁止云端回退</option>
                </select>
                <label>图床批量上传策略</label>
                <select
                  value={appConfig.imageBulkUploadMode || defaultConfig.imageBulkUploadMode}
                  onChange={(event) => setAppConfig((current) => ({ ...current, imageBulkUploadMode: event.target.value as ImageBulkUploadMode }))}
                >
                  <option value="p2p_bulk_https_light">批量走 P2P，轻量走 HTTPS</option>
                  <option value="https_only">全部走 HTTPS</option>
                </select>
                <div className="action-row wrap">
                  <button className="primary" type="button" onClick={() => void saveP2PSettings()} disabled={p2pBusy}>
                    {p2pBusy ? "保存中..." : "保存 P2P 设置"}
                  </button>
                </div>
              </section>
              <section className="card">
                <h2>EasyTier 运行态</h2>
                <div className="state-row"><span>当前状态</span><span>{p2pRuntimeLabel(p2pStatus)}</span></div>
                <div className="state-row"><span>进程 PID</span><span>{p2pStatus?.pid ?? "未运行"}</span></div>
                <div className="state-row"><span>最近启动</span><span>{formatStartedAt(p2pStatus?.startedAt)}</span></div>
                <div className="state-row"><span>参数状态</span><span>{p2pStatus?.configured ? "已配置" : "未配置"}</span></div>
                <label>执行文件</label>
                <div className="mono-box">{p2pStatus?.executablePath || "当前安装包尚未带入 easytier-core.exe"}</div>
                <label>运行目录</label>
                <div className="mono-box">{p2pStatus?.workDir || desktopHostPaths.configDir}</div>
                <label>启动参数摘要</label>
                <div className="mono-box">{p2pStatus?.argsSummary || "当前尚未生成启动参数。先填写下方 EasyTier 节点参数。"}</div>
                {p2pStatus?.lastError ? (
                  <>
                    <label>最近错误</label>
                    <div className="mono-box tone-danger">{p2pStatus.lastError}</div>
                  </>
                ) : null}
                <div className="action-row wrap">
                  <button className="primary" type="button" onClick={() => void handleP2PRuntimeStart()} disabled={p2pBusy || !p2pStatus?.available}>
                    {p2pBusy ? "处理中..." : "启动 EasyTier"}
                  </button>
                  <button className="secondary" type="button" onClick={() => void handleP2PRuntimeStop()} disabled={p2pBusy || !p2pStatus?.running}>
                    停止 EasyTier
                  </button>
                  <button className="secondary" type="button" onClick={() => void refreshP2PStatus()} disabled={p2pBusy}>
                    刷新状态
                  </button>
                </div>
                <div className="action-row wrap">
                  <button className="secondary" type="button" onClick={() => void handleOpenP2PLog("stdout")}>
                    打开 stdout 日志
                  </button>
                  <button className="secondary" type="button" onClick={() => void handleOpenP2PLog("stderr")}>
                    打开 stderr 日志
                  </button>
                </div>
              </section>
            </div>
            <section className="card" style={{ marginTop: 18 }}>
              <h2>EasyTier 节点参数</h2>
              <p className="helper-text">
                这里配置的是用户端 EasyTier 节点本身，不是云端隧道。当前按官方命令行参数生成启动参数，保存后重启 EasyTier 生效。
              </p>
              <label>网络名</label>
              <input
                value={appConfig.p2pNetworkName || ""}
                onChange={(event) => setAppConfig((current) => ({ ...current, p2pNetworkName: event.target.value }))}
                placeholder="例如 cloud-relay"
              />
              <label>网络密钥</label>
              <input
                type="password"
                value={appConfig.p2pNetworkSecret || ""}
                onChange={(event) => setAppConfig((current) => ({ ...current, p2pNetworkSecret: event.target.value }))}
                placeholder="用于同一 EasyTier 网络鉴权"
              />
              <label>初始对等节点</label>
              <textarea
                rows={3}
                value={appConfig.p2pPeerUrl || ""}
                onChange={(event) => setAppConfig((current) => ({ ...current, p2pPeerUrl: event.target.value }))}
                placeholder={"例如 tcp://public.easytier.top:11010\n可填多个，支持换行或逗号分隔"}
              />
              <label className="check-row">
                <input
                  type="checkbox"
                  checked={appConfig.p2pUseDhcp !== false}
                  onChange={(event) => setAppConfig((current) => ({ ...current, p2pUseDhcp: event.target.checked }))}
                />
                <span>使用 DHCP 自动分配虚拟 IPv4</span>
              </label>
              <label>固定虚拟 IPv4</label>
              <input
                value={appConfig.p2pVirtualIpv4 || ""}
                onChange={(event) => setAppConfig((current) => ({ ...current, p2pVirtualIpv4: event.target.value }))}
                placeholder="关闭 DHCP 时必填，例如 10.144.144.23"
                disabled={appConfig.p2pUseDhcp !== false}
              />
              <div className="grid two compact-grid">
                <div>
                  <label>实例名</label>
                  <input
                    value={appConfig.p2pInstanceName || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, p2pInstanceName: event.target.value }))}
                    placeholder="默认 cloud-relay-user"
                  />
                </div>
                <div>
                  <label>主机名</label>
                  <input
                    value={appConfig.p2pHostname || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, p2pHostname: event.target.value }))}
                    placeholder="可选，便于识别用户端节点"
                  />
                </div>
              </div>
              <p className="inline-note">
                打包机需要把 `easytier-core.exe` 放进用户端安装包的 `runtime/` 目录中；当前页面会直接检查该文件是否已被带入。
              </p>
            </section>
          </>
        ) : null}

        {!initializing && user && page === "settings" ? (
          <>
            <div className="page-header">
              <h1>设置</h1>
              <p>用户端只管理用户侧访问、P2P 和本地体验，不承担服务端发布工作。</p>
            </div>
            <div className="grid two">
              <section className="card">
                <h2>连接与行为</h2>
                <label>云端地址</label>
                <input value={apiUrl} onChange={(event) => setApiUrl(event.target.value)} placeholder={defaultApiUrl} />
                <label>关闭行为</label>
                <select
                  value={appConfig.closeAction || defaultConfig.closeAction}
                  onChange={(event) => {
                    const next = { ...appConfig, closeAction: event.target.value as CloseAction };
                    setAppConfig(next);
                  }}
                >
                  <option value="ask">每次询问</option>
                  <option value="tray">最小化到托盘</option>
                  <option value="exit">直接退出</option>
                </select>
                <div className="action-row">
                  <button className="primary" type="button" onClick={() => void saveAndReconnect()} disabled={busy}>保存并重连</button>
                </div>
              </section>
              <section className="card">
                <h2>本地信息</h2>
                <div className="state-row"><span>配置目录</span><span>{desktopHostPaths.configDir}</span></div>
                <div className="state-row"><span>日志目录</span><span>{desktopHostPaths.logDir}</span></div>
                <div className="state-row"><span>当前账号</span><span>{user.email}</span></div>
                <div className="state-row"><span>当前角色</span><span>{roleLabel(user.role)}</span></div>
              </section>
            </div>
            {loginProfiles?.profiles.length ? (
              <section className="card" style={{ marginTop: 18 }}>
                <h2>登录历史</h2>
                {loginProfiles.profiles.map((profile) => (
                  <div key={profile.email} className="history-row">
                    <span>{profile.email}{profile.autoLogin ? " (自动登录)" : ""}</span>
                    <button className="danger small" type="button" onClick={() => setDeleteConfirmTarget(profile.email)}>移除</button>
                  </div>
                ))}
              </section>
            ) : null}
          </>
        ) : null}
      </main>

      {closeDialogOpen ? (
        <div className="modal-overlay" onClick={(event) => event.target === event.currentTarget && setCloseDialogOpen(false)}>
          <div className="modal-dialog">
            <h3>关闭确认</h3>
            <p>用户端关闭后，你仍可以在托盘中保持连接，或者直接退出应用。</p>
            <CloseDialogChoice onClick={handleCloseDialogChoice} onCancel={() => setCloseDialogOpen(false)} />
          </div>
        </div>
      ) : null}

      {reopenDialogOpen ? (
        <div className="modal-overlay" onClick={(event) => event.target === event.currentTarget && setReopenDialogOpen(false)}>
          <div className="modal-dialog">
            <h3>用户端已打开</h3>
            <p>当前已有一个用户端窗口。此版本先保持单窗口工作流。</p>
            <div className="close-dialog-buttons">
              <button className="primary" type="button" onClick={() => handleReopenChoice("cancel")}>知道了</button>
            </div>
          </div>
        </div>
      ) : null}

      {deleteConfirmTarget ? (
        <div className="modal-overlay" onClick={(event) => event.target === event.currentTarget && setDeleteConfirmTarget(null)}>
          <div className="modal-dialog">
            <h3>确认删除</h3>
            <p>确定删除账号 {deleteConfirmTarget} 的登录记录？</p>
            <div className="close-dialog-buttons">
              <button className="secondary dialog-btn" type="button" onClick={() => setDeleteConfirmTarget(null)}>取消</button>
              <button
                className="danger dialog-btn"
                type="button"
                onClick={() => {
                  const email = deleteConfirmTarget;
                  setDeleteConfirmTarget(null);
                  if (email) {
                    void handleDeleteProfile(email);
                  }
                }}
              >
                删除
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function CloseDialogChoice({
  onClick,
  onCancel,
}: {
  onClick: (action: "tray" | "exit", dontAskAgain: boolean) => void;
  onCancel: () => void;
}) {
  const [dontAsk, setDontAsk] = useState(false);
  return (
    <div className="close-dialog-actions">
      <label className="close-dialog-remember">
        <input type="checkbox" checked={dontAsk} onChange={(event) => setDontAsk(event.target.checked)} />
        <span>以后不再询问</span>
      </label>
      <div className="close-dialog-buttons">
        <button className="primary" type="button" onClick={() => onClick("tray", dontAsk)}>最小化到托盘</button>
        <button className="secondary dialog-btn" type="button" onClick={() => onClick("exit", dontAsk)}>退出程序</button>
        <button className="secondary dialog-btn" type="button" onClick={onCancel}>取消</button>
      </div>
    </div>
  );
}
