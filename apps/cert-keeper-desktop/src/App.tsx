import { useState, useEffect, useCallback, useMemo } from "react";
import type { MouseEvent } from "react";
import { listen } from "@tauri-apps/api/event";
import { invoke } from "@tauri-apps/api/core";
import * as api from "./api";
import { decryptLoginPassword, deleteLoginProfile, loadAppConfig, readLoginProfiles, saveAppConfig, saveLoginProfile, type AppConfig, type LoginProfilesFile } from "./desktopHost";
import type { Certificate, DNSCheckResult, User } from "./types";

type Page = "loading" | "certs" | "add" | "setup" | "login" | "settings";
type ReopenDialogChoice = "cancel" | "new-window";
type CloseAction = "ask" | "tray" | "exit";

const legacyDefaultApiUrl = "http://82.156.236.104:7720";
const defaultApiUrl = "https://cert.manage.020309.top";
const defaultCloseAction: CloseAction = "ask";

function normalizeApiBaseUrl(value?: string) {
  const normalized = (value || "").trim();
  if (!normalized || normalized === legacyDefaultApiUrl) {
    return defaultApiUrl;
  }
  return normalized;
}

export default function App() {
  const [page, setPage] = useState<Page>("loading");
  const [user, setUser] = useState<User | null>(null);
  const [certs, setCerts] = useState<Certificate[]>([]);
  const [loading, setLoading] = useState(false);
  const [initializing, setInitializing] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [selectedCert, setSelectedCert] = useState<Certificate | null>(null);
  const [closeDialogOpen, setCloseDialogOpen] = useState(false);
  const [reopenDialogOpen, setReopenDialogOpen] = useState(false);
  const [deleteConfirmTarget, setDeleteConfirmTarget] = useState<string | null>(null);
  const [updatingCertId, setUpdatingCertId] = useState<string | null>(null);
  const [appConfig, setAppConfig] = useState<AppConfig>({ apiBaseUrl: defaultApiUrl, closeAction: defaultCloseAction });
  const [loginProfiles, setLoginProfiles] = useState<LoginProfilesFile | null>(null);
  const [savePassword, setSavePassword] = useState(false);
  const [autoLogin, setAutoLogin] = useState(false);
  const [profileListOpen, setProfileListOpen] = useState(false);

  const [apiUrl, setApiUrl] = useState(defaultApiUrl);
  const [bootstrapSecret, setBootstrapSecret] = useState("");
  const [loginEmail, setLoginEmail] = useState("");
  const [loginPassword, setLoginPassword] = useState("");
  const [addDomain, setAddDomain] = useState("");
  const [addCertPem, setAddCertPem] = useState("");
  const [addKeyPem, setAddKeyPem] = useState("");
  const [addMode, setAddMode] = useState<"manual" | "auto">("auto");
  const [dnsResult, setDnsResult] = useState<DNSCheckResult | null>(null);

  const refreshLoginProfiles = useCallback(async () => {
    const profiles = await readLoginProfiles();
    setLoginProfiles(profiles);
    return profiles;
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

  const persistConfig = useCallback(async (nextConfig: AppConfig) => {
    const normalized: AppConfig = {
      apiBaseUrl: normalizeApiBaseUrl(nextConfig.apiBaseUrl),
      closeAction: (nextConfig.closeAction || defaultCloseAction) as CloseAction,
    };
    setAppConfig(normalized);
    setApiUrl(normalized.apiBaseUrl || defaultApiUrl);
    api.setBaseUrl(normalized.apiBaseUrl || defaultApiUrl);
    await saveAppConfig(normalized);
  }, []);

  const checkAuth = useCallback(async (targetApiUrl?: string): Promise<"authed" | "setup" | "login"> => {
    const normalizedApiUrl = normalizeApiBaseUrl(targetApiUrl || apiUrl);
    api.setBaseUrl(normalizedApiUrl);
    try {
      const currentUser = await api.me();
      setUser(currentUser);
      setPage("certs");
      setError("");
      return "authed";
    } catch {
      try {
        await api.refresh();
        const currentUser = await api.me();
        setUser(currentUser);
        setPage("certs");
        setError("");
        return "authed";
      } catch {
        try {
          const bootstrap = await api.bootstrapStatus();
          setUser(null);
          setPage(bootstrap.required ? "setup" : "login");
          setError("");
          return bootstrap.required ? "setup" : "login";
        } catch (authError) {
          setUser(null);
          setPage("login");
          setError(authError instanceof Error ? authError.message : "无法连接证书服务");
          return "login";
        }
      }
    }
  }, [apiUrl]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [config, profiles] = await Promise.all([
          loadAppConfig(),
          readLoginProfiles(),
        ]);
        if (cancelled) return;
        const normalizedApiUrl = normalizeApiBaseUrl(config?.apiBaseUrl || defaultApiUrl);
        const closeAction = (config?.closeAction || defaultCloseAction) as CloseAction;
        const nextConfig = { apiBaseUrl: normalizedApiUrl, closeAction };
        setAppConfig(nextConfig);
        setApiUrl(normalizedApiUrl);
        api.setBaseUrl(normalizedApiUrl);
        setLoginProfiles(profiles);

        const lastEmail = profiles?.lastUsedEmail || profiles?.profiles[0]?.email || "";
        const lastProfile = lastEmail ? profiles?.profiles.find((item) => item.email === lastEmail) : null;
        let lastPassword = "";
        if (lastEmail) {
          setLoginEmail(lastEmail);
        }
        if (lastProfile) {
          try {
            lastPassword = await decryptLoginPassword(lastEmail);
          } catch {
            lastPassword = "";
          }
          if (cancelled) return;
          setLoginPassword(lastPassword);
          setSavePassword(Boolean(lastPassword));
          setAutoLogin(Boolean(lastPassword) && lastProfile.autoLogin);
        }

        const authState = await checkAuth(normalizedApiUrl);
        if (cancelled) return;
        if (authState === "login" && lastProfile?.autoLogin && lastPassword) {
          try {
            const loginUser = await api.login(lastEmail, lastPassword);
            if (cancelled) return;
            setUser(loginUser);
            setPage("certs");
            setError("");
          } catch {
            if (!cancelled) {
              setPage("login");
              setError("");
            }
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
  }, [checkAuth]);

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

  const loadCerts = async () => {
    setLoading(true);
    try {
      const items = await api.listCertificates();
      setCerts(items);
    } catch (e: any) {
      setError(e instanceof Error ? e.message : String(e));
    }
    setLoading(false);
  };

  useEffect(() => {
    if (page === "certs" && user) {
      void loadCerts();
    }
  }, [page, user]);

  const saveConfig = async (url: string) => {
    await persistConfig({ ...appConfig, apiBaseUrl: normalizeApiBaseUrl(url) });
  };

  const setCloseAction = async (value: CloseAction) => {
    await persistConfig({ ...appConfig, closeAction: value });
  };

  const rememberProfile = useCallback(async (email: string, password: string, nextSavePassword: boolean, nextAutoLogin: boolean) => {
    try {
      await saveLoginProfile(email, nextSavePassword ? password : "", nextSavePassword && nextAutoLogin);
      await refreshLoginProfiles();
    } catch {
      // ignore remember failures
    }
  }, [refreshLoginProfiles]);

  const handleSetup = async () => {
    setLoading(true);
    setError("");
    try {
      await saveConfig(apiUrl);
      const bootstrapUser = await api.bootstrap(loginEmail, "Admin", loginPassword, bootstrapSecret);
      setUser(bootstrapUser);
      setBootstrapSecret("");
      setPage("certs");
      await rememberProfile(loginEmail, loginPassword, savePassword, autoLogin);
    } catch (e: any) {
      setError(e instanceof Error ? e.message : String(e));
    }
    setLoading(false);
  };

  const handleLogin = async () => {
    setLoading(true);
    setError("");
    try {
      await saveConfig(apiUrl);
      const loginUser = await api.login(loginEmail, loginPassword);
      setUser(loginUser);
      setPage("certs");
      await rememberProfile(loginEmail, loginPassword, savePassword, autoLogin);
      setProfileListOpen(false);
    } catch (e: any) {
      setError(e instanceof Error ? e.message : String(e));
    }
    setLoading(false);
  };

  const handleLogout = async () => {
    try {
      await api.logout();
    } catch {
      // ignore logout failures
    }
    setUser(null);
    setSelectedCert(null);
    setCerts([]);
    setPage("login");
  };

  const handleAutoIssue = async () => {
    setLoading(true);
    setError("");
    setNotice("");
    try {
      const created = await api.autoIssue(addDomain);
      await loadCerts();
      setSelectedCert(created);
      setAddDomain("");
      setDnsResult(null);
      setPage("certs");
      setNotice(`证书已签发：${created.domain}`);
    } catch (e: any) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };

  const handleManualAdd = async () => {
    setLoading(true);
    setError("");
    try {
      await api.createCertificate(addDomain, addCertPem, addKeyPem);
      setAddDomain("");
      setAddCertPem("");
      setAddKeyPem("");
      setPage("certs");
    } catch (e: any) {
      setError(e instanceof Error ? e.message : String(e));
    }
    setLoading(false);
  };

  const handleDnsCheck = async () => {
    try {
      const result = await api.dnsCheck(addDomain);
      setDnsResult(result);
    } catch {
      setDnsResult({ resolved: false, ips: [], matches: false });
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("确定要删除此证书吗？")) return;
    try {
      await api.deleteCertificate(id);
      await loadCerts();
      if (selectedCert?.id === id) setSelectedCert(null);
    } catch (e: any) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const handleAutoRenewToggle = async (cert: Certificate, nextValue: boolean) => {
    setUpdatingCertId(cert.id);
    setError("");
    setNotice("");
    try {
      const updated = await api.updateCertificate(cert.id, { autoRenew: nextValue });
      setCerts((items) => items.map((item) => item.id === updated.id ? updated : item));
      setSelectedCert(updated);
      setNotice(`${updated.domain} 已${updated.autoRenew ? "开启" : "关闭"}自动续期`);
    } catch (e: any) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setUpdatingCertId(null);
    }
  };

  const handleDeleteProfile = async (email: string) => {
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
      if (!updated?.profiles.length) {
        setProfileListOpen(false);
      }
    } catch (e: any) {
      setError(e.message || "删除失败");
    }
  };

  const handleWindowClose = () => {
    const closeAction = (appConfig.closeAction || defaultCloseAction) as CloseAction;
    if (closeAction === "ask") {
      setCloseDialogOpen(true);
      return;
    }
    if (closeAction === "tray") {
      void invoke("window_request_close");
      return;
    }
    void invoke("app_exit");
  };

  const handleCloseDialogChoice = async (action: "tray" | "exit", dontAskAgain: boolean) => {
    setCloseDialogOpen(false);
    if (dontAskAgain) {
      await setCloseAction(action);
    }
    if (action === "tray") {
      void invoke("window_request_close");
      return;
    }
    void invoke("app_exit");
  };

  const handleReopenChoice = async (choice: ReopenDialogChoice) => {
    setReopenDialogOpen(false);
    if (choice !== "new-window") return;
    try {
      await invoke("open_additional_window");
    } catch (error) {
      setError(error instanceof Error ? error.message : "打开新客户端失败");
    }
  };

  const handleTitlebarDrag = (event: MouseEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    const target = event.target as HTMLElement | null;
    if (target?.closest("button, input, select, textarea, a, label")) return;
    void invoke("window_start_drag");
  };

  const daysUntil = (dateStr?: string) => {
    if (!dateStr) return null;
    const diff = new Date(dateStr).getTime() - Date.now();
    return Math.ceil(diff / 86400000);
  };

  const selectedCertLive = useMemo(() => {
    if (!selectedCert) return null;
    return certs.find((item) => item.id === selectedCert.id) || selectedCert;
  }, [certs, selectedCert]);

  const selectedCertSupportsAutoRenew = (selectedCertLive?.issuer || "").toLowerCase() === "letsencrypt";

  const statusBadge = (cert: Certificate) => {
    const days = daysUntil(cert.expiresAt);
    if (cert.renewError) return <span className="badge badge-error">续期失败</span>;
    if (days === null) return <span className="badge badge-unknown">未知</span>;
    if (days < 0) return <span className="badge badge-expired">已过期</span>;
    if (days < 14) return <span className="badge badge-warn">即将过期</span>;
    return <span className="badge badge-ok">正常</span>;
  };

  return (
    <div className="app">
      <div className="titlebar" onMouseDown={handleTitlebarDrag}>
        <span className="titlebar-text">证书管家</span>
        <div className="titlebar-actions">
          <button onClick={() => invoke("window_minimize")} className="tb-btn">─</button>
          <button onClick={() => invoke("window_toggle_maximize")} className="tb-btn">□</button>
          <button onClick={handleWindowClose} className="tb-btn close">×</button>
        </div>
      </div>

      <div className="sidebar">
        <div className="sidebar-brand">证书管家</div>
        {user && <>
          <button className={`nav-btn ${page === "certs" ? "active" : ""}`} onClick={() => setPage("certs")}>证书列表</button>
          <button className={`nav-btn ${page === "add" ? "active" : ""}`} onClick={() => { setPage("add"); setAddMode("auto"); setDnsResult(null); setError(""); }}>添加证书</button>
          <button className={`nav-btn ${page === "settings" ? "active" : ""}`} onClick={() => setPage("settings")}>设置</button>
          <div className="sidebar-footer">
            <div className="user-info">{user.email}</div>
            <button className="logout-btn" onClick={handleLogout}>退出</button>
          </div>
        </>}
      </div>

      <div className="main">
        {error && <div className="error-bar" onClick={() => setError("")}>{error}</div>}
        {notice && <div className="success-bar" onClick={() => setNotice("")}>{notice}</div>}

        {initializing || page === "loading" ? (
          <div className="card">
            <h2>初始化中</h2>
            <p className="helper-text">正在读取配置并检查登录状态…</p>
          </div>
        ) : null}

        {page === "setup" && !initializing && (
          <div className="card">
            <h2>初始化管理员</h2>
            <label>API 地址</label>
            <input value={apiUrl} onChange={e => setApiUrl(e.target.value)} placeholder="https://cert.manage.020309.top" />
            <div className="helper-text">建议使用证书管家专用域名入口，不再直接暴露公网 IP 或裸端口。</div>
            <label>引导密钥</label>
            <input value={bootstrapSecret} onChange={e => setBootstrapSecret(e.target.value)} placeholder="X-Bootstrap-Secret" />
            <label>邮箱</label>
            <input value={loginEmail} onChange={e => setLoginEmail(e.target.value)} />
            <label>密码</label>
            <input type="password" value={loginPassword} onChange={e => setLoginPassword(e.target.value)} />
            <label className="login-check-row">
              <input type="checkbox" checked={savePassword} onChange={(event) => {
                if (autoLogin && !event.target.checked) return;
                setSavePassword(event.target.checked);
              }} disabled={autoLogin} />
              <span>保存密码{autoLogin ? "（自动登录需要）" : ""}</span>
            </label>
            <label className="login-check-row">
              <input type="checkbox" checked={autoLogin} onChange={(event) => {
                const next = event.target.checked;
                setAutoLogin(next);
                if (next) setSavePassword(true);
              }} />
              <span>自动登录</span>
            </label>
            <button className="primary" disabled={loading} onClick={handleSetup}>{loading ? "处理中..." : "创建管理员"}</button>
          </div>
        )}

        {page === "login" && !initializing && (
          <div className="card">
            <h2>登录</h2>
            <label>API 地址</label>
            <input value={apiUrl} onChange={e => setApiUrl(e.target.value)} placeholder="https://cert.manage.020309.top" />
            <div className="helper-text">默认推荐走证书管家专用域名入口。</div>
            <label>邮箱</label>
            {loginProfiles && loginProfiles.profiles.length > 0 ? (
              <div className="profile-selector">
                <div className="profile-input-wrap">
                  <input value={loginEmail} onChange={e => setLoginEmail(e.target.value)} onFocus={() => setProfileListOpen(false)} />
                  <button type="button" className="profile-arrow-btn" onClick={() => setProfileListOpen((current) => !current)}>
                    <i className={`fas fa-chevron-${profileListOpen ? "up" : "down"}`} />
                  </button>
                </div>
                {profileListOpen ? (
                  <div className="profile-dropdown">
                    {loginProfiles.profiles.map((profile) => (
                      <div key={profile.email} className={`profile-row${loginEmail === profile.email ? " selected" : ""}`} onClick={() => void fillProfileCredentials(profile.email)}>
                        <span className="profile-email">{profile.email}{profile.autoLogin ? " (自动登录)" : ""}</span>
                        <button type="button" className="profile-delete-btn" onClick={(event) => {
                          event.stopPropagation();
                          setDeleteConfirmTarget(profile.email);
                        }}><i className="fas fa-trash-alt" /></button>
                      </div>
                    ))}
                  </div>
                ) : null}
              </div>
            ) : (
              <input value={loginEmail} onChange={e => setLoginEmail(e.target.value)} />
            )}
            <label>密码</label>
            <input type="password" value={loginPassword} onChange={e => setLoginPassword(e.target.value)} />
            <label className="login-check-row">
              <input type="checkbox" checked={savePassword} onChange={(event) => {
                if (autoLogin && !event.target.checked) return;
                setSavePassword(event.target.checked);
              }} disabled={autoLogin} />
              <span>保存密码{autoLogin ? "（自动登录需要）" : ""}</span>
            </label>
            <label className="login-check-row">
              <input type="checkbox" checked={autoLogin} onChange={(event) => {
                const next = event.target.checked;
                setAutoLogin(next);
                if (next) setSavePassword(true);
              }} />
              <span>自动登录</span>
            </label>
            <button className="primary" disabled={loading} onClick={handleLogin}>{loading ? "登录中..." : "登录"}</button>
          </div>
        )}

        {page === "certs" && (
          <div className="cert-list">
            <div className="cert-list-header">
              <h2>证书列表</h2>
              <button className="primary small" onClick={() => { setPage("add"); setAddMode("auto"); setDnsResult(null); setError(""); }}>+ 添加</button>
            </div>
            {loading && <div className="loading">加载中...</div>}
            {certs.length === 0 && !loading && <div className="empty">暂无证书，点击添加</div>}
            <table className="cert-table">
              <thead><tr><th>域名</th><th>签发方</th><th>到期</th><th>状态</th><th>操作</th></tr></thead>
              <tbody>
                {certs.map(c => (
                  <tr key={c.id} onClick={() => setSelectedCert(c)} className={selectedCertLive?.id === c.id ? "selected" : ""}>
                    <td>{c.domain}</td>
                    <td>{c.issuer || "手动上传"}</td>
                    <td>{c.expiresAt ? new Date(c.expiresAt).toLocaleDateString() : "—"}</td>
                    <td>{statusBadge(c)}</td>
                    <td><button className="danger small" onClick={e => { e.stopPropagation(); void handleDelete(c.id); }}>删除</button></td>
                  </tr>
                ))}
              </tbody>
            </table>
            {selectedCertLive && (
              <div className="cert-detail">
                <h3>{selectedCertLive.domain}</h3>
                <div className="detail-grid">
                  <span className="label">ID</span><span>{selectedCertLive.id}</span>
                  <span className="label">签发方</span><span>{selectedCertLive.issuer || "手动上传"}</span>
                  <span className="label">到期时间</span><span>{selectedCertLive.expiresAt ? new Date(selectedCertLive.expiresAt).toLocaleString() : "未知"}</span>
                  <span className="label">自动续期</span>
                  {selectedCertSupportsAutoRenew ? (
                    <label className={`toggle-row${updatingCertId === selectedCertLive.id ? " disabled" : ""}`}>
                      <input
                        type="checkbox"
                        checked={selectedCertLive.autoRenew}
                        disabled={updatingCertId === selectedCertLive.id}
                        onChange={(event) => void handleAutoRenewToggle(selectedCertLive, event.target.checked)}
                      />
                      <span>{selectedCertLive.autoRenew ? "已开启" : "已关闭"}</span>
                    </label>
                  ) : (
                    <span>仅 Let's Encrypt 证书支持</span>
                  )}
                  <span className="label">DNS验证</span><span>{selectedCertLive.dnsVerifiedAt ? new Date(selectedCertLive.dnsVerifiedAt).toLocaleString() : "未验证"}</span>
                  <span className="label">上次续期</span><span>{selectedCertLive.lastRenewedAt ? new Date(selectedCertLive.lastRenewedAt).toLocaleString() : "—"}</span>
                  {selectedCertLive.renewError && <><span className="label">续期错误</span><span className="error-text">{selectedCertLive.renewError}</span></>}
                </div>
              </div>
            )}
          </div>
        )}

        {page === "add" && (
          <div className="card">
            <h2>添加证书</h2>
            <div className="tab-bar">
              <button className={`tab ${addMode === "auto" ? "active" : ""}`} onClick={() => setAddMode("auto")}>自动签发</button>
              <button className={`tab ${addMode === "manual" ? "active" : ""}`} onClick={() => setAddMode("manual")}>手动上传</button>
            </div>

            <label>域名</label>
            <input value={addDomain} onChange={e => setAddDomain(e.target.value)} placeholder="example.com" />

            {addMode === "auto" && (
              <>
                <button className="secondary" disabled={!addDomain || loading} onClick={() => void handleDnsCheck()}>检查DNS</button>
                {dnsResult && (
                  <div className={`dns-result ${dnsResult.matches ? "ok" : "fail"}`}>
                    {dnsResult.resolved
                      ? dnsResult.matches
                        ? `✓ DNS解析正确 (${dnsResult.ips.join(", ")})`
                        : `✗ DNS解析到 ${dnsResult.ips.join(", ")}，不匹配服务器IP`
                      : "✗ 域名无法解析"
                    }
                  </div>
                )}
                {loading ? <div className="loading-inline">正在向 Let's Encrypt 发起验证并签发证书，请稍候…</div> : null}
                <button className={`primary${loading ? " loading-busy" : ""}`} disabled={!addDomain || loading} onClick={() => void handleAutoIssue()}>
                  {loading ? "签发中..." : "自动签发证书"}
                </button>
              </>
            )}

            {addMode === "manual" && (
              <>
                <label>Certificate PEM</label>
                <textarea rows={6} value={addCertPem} onChange={e => setAddCertPem(e.target.value)} placeholder="-----BEGIN CERTIFICATE-----" />
                <label>Private Key PEM</label>
                <textarea rows={6} value={addKeyPem} onChange={e => setAddKeyPem(e.target.value)} placeholder="-----BEGIN PRIVATE KEY-----" />
                <button className="primary" disabled={!addDomain || !addCertPem || !addKeyPem || loading} onClick={() => void handleManualAdd()}>
                  {loading ? "上传中..." : "上传证书"}
                </button>
              </>
            )}
          </div>
        )}

        {page === "settings" && (
          <div className="card">
            <h2>设置</h2>
            <label>API 地址</label>
            <input value={apiUrl} onChange={e => setApiUrl(e.target.value)} placeholder="https://cert.manage.020309.top" />
            <div className="helper-text">建议优先使用专用管理域名接入证书管家。</div>
            <label>关闭行为</label>
            <select className="settings-select" value={appConfig.closeAction || defaultCloseAction} onChange={event => void setCloseAction(event.target.value as CloseAction)}>
              <option value="ask">每次询问</option>
              <option value="tray">最小化到托盘</option>
              <option value="exit">直接退出</option>
            </select>
            <button className="primary" onClick={() => void saveConfig(apiUrl).then(() => checkAuth(apiUrl))}>保存并重连</button>
            {loginProfiles && loginProfiles.profiles.length > 0 ? (
              <>
                <div className="settings-section-label">登录历史</div>
                {loginProfiles.profiles.map((profile) => (
                  <div key={profile.email} className="settings-history-row">
                    <span>{profile.email}{profile.autoLogin ? " (自动登录)" : ""}</span>
                    <button className="danger small" type="button" onClick={() => setDeleteConfirmTarget(profile.email)}>移除</button>
                  </div>
                ))}
              </>
            ) : null}
          </div>
        )}
      </div>

      {closeDialogOpen ? (
        <div className="modal-overlay" onClick={(event) => event.target === event.currentTarget && setCloseDialogOpen(false)}>
          <div className="modal-dialog">
            <h3>关闭确认</h3>
            <p>您希望关闭时如何处理？</p>
            <CloseDialogChoice onClick={handleCloseDialogChoice} onCancel={() => setCloseDialogOpen(false)} />
          </div>
        </div>
      ) : null}

      {reopenDialogOpen ? (
        <div className="modal-overlay" onClick={(event) => event.target === event.currentTarget && setReopenDialogOpen(false)}>
          <div className="modal-dialog">
            <h3>证书管家已打开</h3>
            <p>当前已有一个证书管家窗口。是否再打开一个新的客户端窗口？</p>
            <ReopenDialogChoice onClick={handleReopenChoice} onCancel={() => setReopenDialogOpen(false)} />
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
              <button className="danger dialog-btn" type="button" onClick={() => {
                const email = deleteConfirmTarget;
                setDeleteConfirmTarget(null);
                void handleDeleteProfile(email);
              }}>删除</button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function CloseDialogChoice({ onClick, onCancel }: { onClick: (action: "tray" | "exit", dontAskAgain: boolean) => void; onCancel: () => void }) {
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

function ReopenDialogChoice({ onClick, onCancel }: { onClick: (choice: ReopenDialogChoice) => void; onCancel: () => void }) {
  return (
    <div className="close-dialog-actions">
      <div className="close-dialog-buttons">
        <button className="primary" type="button" onClick={() => onClick("new-window")}>打开新客户端</button>
        <button className="secondary dialog-btn" type="button" onClick={onCancel}>取消</button>
      </div>
    </div>
  );
}
