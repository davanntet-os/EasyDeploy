import { useEffect, useMemo, useRef, useState } from "react";
import {
  api,
  auth,
  environment,
  setUnauthorizedHandler,
  wsURL,
  type Container,
  type ContainerInspect,
  type Route,
  type Tunnel,
  type DeployRequest,
  type Registry,
  type DockerNetwork,
  type DockerVolume,
  type VolFile,
  type Endpoint,
  type EdgeStatus,
  type Me,
  type User,
  type ResourceRequest,
  type Role,
  type Service,
  type ServiceRequest,
} from "./api";
import { readRoute, setRouteTab, onRouteChange } from "./route";
import { Icon } from "./icons";
import { ActivityBar, Toaster, toast, run, useAction } from "./ui";
import { Shell } from "./Terminal";
import { Monitor } from "./Monitor";
import { AdvancedPanel, emptyAdvForm, buildAdvanced, advToForm, type AdvForm } from "./ServiceAdvanced";
import { Section, Field, SourceSelector, SearchPicker } from "./FormKit";

type Tab =
  | "overview"
  | "containers"
  | "services"
  | "networks"
  | "volumes"
  | "routes"
  | "registries"
  | "expose"
  | "environments"
  | "users"
  | "requests"
  | "docs";

const TAB_META: Record<Tab, { icon: (p: { size?: number }) => JSX.Element; label: string }> = {
  overview: { icon: Icon.Gauge, label: "Overview" },
  containers: { icon: Icon.Box, label: "Containers" },
  services: { icon: Icon.Layers, label: "Services" },
  networks: { icon: Icon.Network, label: "Networks" },
  volumes: { icon: Icon.Drive, label: "Volumes" },
  routes: { icon: Icon.Route, label: "Routes" },
  registries: { icon: Icon.Registry, label: "Registries" },
  expose: { icon: Icon.Globe, label: "Expose" },
  environments: { icon: Icon.Server, label: "Environments" },
  users: { icon: Icon.Users, label: "Users" },
  requests: { icon: Icon.Inbox, label: "Requests" },
  docs: { icon: Icon.Book, label: "Docs" },
};

// Tabs visible per role. Members get a reduced surface centered on their
// services and resource requests.
const ADMIN_TABS: Tab[] = ["overview", "containers", "services", "networks", "volumes", "routes", "registries", "expose", "environments", "users", "requests", "docs"];
const MEMBER_TABS: Tab[] = ["overview", "containers", "services", "networks", "volumes", "registries", "requests", "docs"];

export function App() {
  const [authed, setAuthed] = useState<boolean>(!!auth.get());

  useEffect(() => {
    setUnauthorizedHandler(() => setAuthed(false));
  }, []);

  if (!authed) return <Login onLogin={() => setAuthed(true)} />;
  return <Dashboard onLogout={() => { auth.clear(); setAuthed(false); }} />;
}

function Login({ onLogin }: { onLogin: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      const { token } = await api.login(username, password);
      auth.set(token);
      onLogin();
    } catch (e) {
      setErr(String((e as Error).message || e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login">
      <form className="login-card" onSubmit={submit}>
        <span className="brand-mark login-mark">
          <Icon.Logo size={26} />
        </span>
        <h1>EasyDeploy</h1>
        <p className="muted">Sign in to manage your containers.</p>
        <input
          autoFocus
          placeholder="Username"
          autoComplete="username"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
        <input
          type="password"
          placeholder="Password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {err && <p className="error">{err}</p>}
        <button type="submit" className="primary" disabled={busy}>
          {busy ? <Icon.Spinner size={15} /> : null}
          <span>{busy ? "Signing in…" : "Sign in"}</span>
        </button>
      </form>
    </div>
  );
}

// Tabs that only make sense on the local host. Services and Routes now work on
// remote hosts too (via a per-host edge Envoy), so only public Expose — which
// depends on the local host's own network/tunnel — stays local-only.
const LOCAL_ONLY_TABS: Tab[] = ["expose"];

// isTab narrows an arbitrary URL fragment to a known tab.
const isTab = (s: string): s is Tab => (ADMIN_TABS as string[]).includes(s);

function Dashboard({ onLogout }: { onLogout: () => void }) {
  const [tab, setTab] = useState<Tab>(() => {
    const t = readRoute().tab;
    return isTab(t) ? t : "overview";
  });
  const [health, setHealth] = useState<{ ok: boolean; dockerError: string } | null>(null);
  const [healthNonce, setHealthNonce] = useState(0);
  const [me, setMe] = useState<Me | null>(null);
  const [drawer, setDrawer] = useState(false);
  const [envId, setEnvId] = useState(environment.get());

  const refreshMe = () => api.me().then(setMe).catch(() => {});
  useEffect(() => {
    refreshMe();
    const offEnv = environment.subscribe(() => setEnvId(environment.get()));
    // Follow the URL on back/forward navigation or a pasted/edited link.
    const offRoute = onRouteChange(() => {
      const t = readRoute().tab;
      if (isTab(t)) setTab(t);
    });
    return () => {
      offEnv();
      offRoute();
    };
  }, []);

  // Health reflects the *selected* environment: the local daemon (/health) or a
  // remote host's reachability (endpointStatus). Re-checked when the env changes
  // so a dead remote host is caught before every list request hangs on it.
  useEffect(() => {
    let live = true;
    setHealth(null);
    const check =
      envId === 0
        ? api.health()
        : api.endpointStatus(envId).then((s) => ({ ok: s.ok, dockerError: s.ok ? "" : "unreachable" }));
    check.then((h) => live && setHealth(h)).catch(() => live && setHealth({ ok: false, dockerError: "unreachable" }));
    return () => {
      live = false;
    };
  }, [envId, healthNonce]);

  const isAdmin = me?.role === "admin";
  const onLocal = envId === 0;
  // A selected remote environment that isn't responding — don't drown the user
  // in perpetual skeletons; show a clear switch-to-local panel instead.
  const envDown = !onLocal && health !== null && !health.ok;
  let tabs = isAdmin ? ADMIN_TABS : MEMBER_TABS;
  if (!onLocal) tabs = tabs.filter((t) => !LOCAL_ONLY_TABS.includes(t));
  // Keep the active tab valid for the role and environment.
  const activeTab = tabs.includes(tab) ? tab : "containers";
  // Normalize the URL when the requested tab isn't valid here (wrong role /
  // remote-hidden tab), so a refresh lands on the same resolved tab.
  useEffect(() => {
    if (readRoute().tab !== activeTab) setRouteTab(activeTab, true);
  }, [activeTab]);
  const go = (t: Tab) => {
    setTab(t);
    setRouteTab(t); // reflect into the URL (adds a history entry)
    setDrawer(false); // close the mobile drawer on navigation
  };

  return (
    <div className={`shell ${drawer ? "drawer-open" : ""}`}>
      <ActivityBar />
      <Toaster />

      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark">
            <Icon.Logo size={20} />
          </span>
          <h1>EasyDeploy</h1>
        </div>
        <nav className="side-nav">
          {tabs.map((t) => {
            const M = TAB_META[t];
            return (
              <button key={t} className={activeTab === t ? "active" : ""} onClick={() => go(t)}>
                <M.icon size={17} />
                <span>{M.label}</span>
              </button>
            );
          })}
        </nav>
        <div className="side-foot">
          <div className={`health-chip ${health ? (health.ok ? "ok" : "bad") : ""}`} title={health?.ok ? "Docker daemon reachable" : health?.dockerError || "Checking connection…"}>
            <span className={`dot ${health ? (health.ok ? "ok" : "bad") : "pending"}`} />
            <span className="health-text">
              <span className="health-title">{health ? (health.ok ? "Connected" : "Disconnected") : "Connecting…"}</span>
              <span className="health-sub">{onLocal ? "Local Docker host" : "Remote environment"}</span>
            </span>
          </div>
        </div>
      </aside>

      <div className="main-col">
        <header className="topbar">
          <button className="menu-btn btn-icon" onClick={() => setDrawer((v) => !v)} aria-label="Menu">
            <Icon.Menu size={18} />
          </button>
          <h2 className="page-title">
            {(() => {
              const M = TAB_META[activeTab];
              return (
                <>
                  <M.icon size={17} />
                  <span>{M.label}</span>
                </>
              );
            })()}
          </h2>
          <div className="topbar-right">
            <EnvSwitcher canManage={!!isAdmin} />
            {me && me.role === "member" && <QuotaPill me={me} />}
            {me && (
              <span className="whoami" title={`Signed in as ${me.username} (${me.role})`}>
                <span className={`role-tag ${me.role}`}>{me.role}</span>
                {me.username}
              </span>
            )}
            <button className="btn-icon logout" onClick={onLogout} title="Log out">
              <Icon.Logout size={16} />
            </button>
          </div>
        </header>

        {/* Keyed by env so switching hosts remounts the views and re-fetches. */}
        <main key={envId}>
          {envDown ? (
            <EnvUnreachable onSwitch={() => environment.set(0)} onRetry={() => setHealthNonce((n) => n + 1)} />
          ) : (
            <>
          {activeTab === "overview" && <Overview me={me} isAdmin={!!isAdmin} onGo={go} />}
          {activeTab === "containers" && <Containers />}
          {activeTab === "services" && <Services me={me} onChanged={refreshMe} />}
          {activeTab === "networks" && <Networks />}
          {activeTab === "volumes" && <Volumes />}
          {activeTab === "routes" && <Routes />}
          {activeTab === "registries" && <Registries />}
          {activeTab === "expose" && <Expose />}
          {activeTab === "environments" && <Environments />}
          {activeTab === "users" && <Users />}
          {activeTab === "requests" && <Requests isAdmin={!!isAdmin} onReviewed={refreshMe} />}
          {activeTab === "docs" && <Docs isAdmin={!!isAdmin} onGo={go} />}
            </>
          )}
        </main>
      </div>

      {drawer && <button className="scrim" aria-label="Close menu" onClick={() => setDrawer(false)} />}
    </div>
  );
}

// dotState maps a possibly-unknown reachability to a status-dot class.
function dotState(ok: boolean | undefined): string {
  if (ok === undefined) return "pending";
  return ok ? "ok" : "bad";
}

// EnvSwitcher is the multi-host environment selector in the topbar. Members see
// only the environments granted to them (backend-filtered) and can't manage.
function EnvSwitcher({ canManage }: { canManage: boolean }) {
  const [list, , reload] = useAsync<Endpoint[]>(() => api.endpoints(), []);
  const [status, setStatus] = useState<Record<number, boolean>>({});
  const [open, setOpen] = useState(false);
  const [adding, setAdding] = useState(false);
  const [curId, setCurId] = useState(environment.get());

  useEffect(() => environment.subscribe(() => setCurId(environment.get())), []);
  useEffect(() => {
    if (!list) return;
    list.forEach((e) => api.endpointStatus(e.id).then((s) => setStatus((p) => ({ ...p, [e.id]: s.ok }))).catch(() => {}));
  }, [list]);

  const current = list?.find((e) => e.id === curId);
  // Nothing to switch between: a member with only the local host doesn't need
  // the switcher at all.
  if (list && list.length <= 1 && !canManage) return null;
  const select = (id: number) => {
    environment.set(id);
    setOpen(false);
  };
  const remove = async (e: Endpoint) => {
    if (!window.confirm(`Remove environment ${e.name}?`)) return;
    const ok = await run(api.deleteEndpoint(e.id), { success: `Removed ${e.name}` });
    if (ok !== undefined) {
      if (curId === e.id) environment.set(0);
      reload();
    }
  };

  return (
    <div className="env-switcher">
      <button type="button" className="env-current" onClick={() => setOpen((o) => !o)}>
        <Icon.Server size={15} />
        <span className="env-cur-name">{current?.name ?? "Local"}</span>
        <span className={`dot ${dotState(status[curId])}`} />
        <Icon.Chevron size={13} />
      </button>
      {open && (
        <>
          <button type="button" className="env-scrim" aria-label="Close" onClick={() => setOpen(false)} />
          <div className="env-menu">
            <div className="env-menu-label">Environments</div>
            {list?.map((e) => (
              <div key={e.id} className={`env-item ${e.id === curId ? "active" : ""}`}>
                <button type="button" className="env-item-main" onClick={() => select(e.id)}>
                  <span className={`dot ${dotState(status[e.id])}`} />
                  <span className="env-name">{e.name}</span>
                  <span className="muted mono env-host">{e.host}</span>
                </button>
                {canManage && !e.local && (
                  <button type="button" className="btn-icon danger" title="Remove" onClick={() => remove(e)}>
                    <Icon.Trash size={13} />
                  </button>
                )}
              </div>
            ))}
            {canManage && (
              <button type="button" className="env-add" onClick={() => { setOpen(false); setAdding(true); }}>
                <Icon.Plus size={14} /> <span>Add environment</span>
              </button>
            )}
          </div>
        </>
      )}
      {adding && <EnvModal onClose={() => setAdding(false)} onAdded={() => { setAdding(false); reload(); }} />}
    </div>
  );
}

type ConnMode = "ssh" | "tls" | "plain";

function EnvModal({ editing, onClose, onAdded }: { editing?: Endpoint; onClose: () => void; onAdded: () => void }) {
  const initialMode: ConnMode = editing
    ? editing.host.startsWith("ssh://")
      ? "ssh"
      : editing.tls
        ? "tls"
        : "plain"
    : "ssh";
  const [mode, setMode] = useState<ConnMode>(initialMode);
  const [f, setF] = useState({ name: editing?.name ?? "", host: editing?.host ?? "", port: "", tlsCa: "", tlsCert: "", tlsKey: "" });
  const [busy, setBusy] = useState(false);

  const hostHint =
    mode === "ssh" ? "ssh://user@10.0.0.5" : mode === "tls" ? "tcp://10.0.0.5:2376" : "tcp://10.0.0.5:2375";

  // For SSH the port is optional (defaults to 22). When given, it overrides any
  // port in the URL; the backend passes it to ssh via the ssh://host:port form.
  const sshHost = () => {
    const p = f.port.trim();
    if (mode !== "ssh" || !p) return f.host.trim();
    const base = f.host.trim().replace(/^ssh:\/\//, "").replace(/:\d+$/, "");
    return `ssh://${base}:${p}`;
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    const host = sshHost();
    // On edit, TLS material is only sent when re-entered (blank preserves the
    // stored certs), so include it only if a cert was typed.
    const body =
      mode === "tls" && (f.tlsCert || !editing)
        ? { name: f.name, host, tlsCa: f.tlsCa, tlsCert: f.tlsCert, tlsKey: f.tlsKey }
        : { name: f.name, host };
    const res = editing
      ? await run(api.updateEndpoint(editing.id, body), { success: `Updated ${f.name}` })
      : await run(api.createEndpoint(body), { success: `Added ${f.name}` });
    setBusy(false);
    if (res) onAdded();
  };

  const modes: { key: ConnMode; icon: (p: { size?: number }) => JSX.Element; title: string; sub: string; rec?: boolean }[] = [
    { key: "ssh", icon: Icon.Terminal, title: "SSH", sub: "Tunnel, no open port", rec: true },
    { key: "tls", icon: Icon.Server, title: "TLS", sub: "TCP + client certs" },
    { key: "plain", icon: Icon.Globe, title: "Plain", sub: "TCP, unencrypted" },
  ];

  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body env-modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong>
            <Icon.Server size={15} /> {editing ? `Edit ${editing.name}` : "Add environment"}
          </strong>
          <button type="button" onClick={onClose} aria-label="Close">
            <Icon.Close size={16} />
          </button>
        </div>
        <form className="form modal-pad" onSubmit={submit}>
          <div className="field">
            <span className="field-label">Connection type</span>
            <div className="conn-cards">
              {modes.map((m) => (
                <button
                  key={m.key}
                  type="button"
                  className={`conn-card ${mode === m.key ? "active" : ""}`}
                  onClick={() => setMode(m.key)}
                >
                  {m.rec && <span className="conn-rec">Recommended</span>}
                  <m.icon size={20} />
                  <span className="conn-title">{m.title}</span>
                  <span className="conn-sub">{m.sub}</span>
                </button>
              ))}
            </div>
          </div>

          <div className="row">
            <Field label="Name" required>
              <input required value={f.name} placeholder="prod-vm" onChange={(e) => setF({ ...f, name: e.target.value })} />
            </Field>
            <Field label="Docker host" required>
              <input required value={f.host} placeholder={hostHint} onChange={(e) => setF({ ...f, host: e.target.value })} />
            </Field>
            {mode === "ssh" && (
              <Field label="SSH port" help="Optional — defaults to 22. Set this if SSH runs on a non-standard port.">
                <input
                  className="port-input"
                  inputMode="numeric"
                  value={f.port}
                  placeholder="22"
                  onChange={(e) => setF({ ...f, port: e.target.value.replace(/[^0-9]/g, "") })}
                />
              </Field>
            )}
          </div>

          {mode === "ssh" && (
            <p className="env-note">
              <Icon.Terminal size={14} />
              <span>
                Uses the EasyDeploy host's SSH keys — no Docker port is opened. The server must be able to{" "}
                <code>
                  ssh {f.port.trim() ? `-p ${f.port.trim()} ` : ""}
                  {f.host.replace(/^ssh:\/\//, "").replace(/:\d+$/, "") || "user@host"}
                </code>
                .
              </span>
            </p>
          )}
          {mode === "plain" && (
            <p className="env-note env-warn">
              <Icon.Alert size={14} />
              <span>Unauthenticated root access — only on a trusted, isolated network. Never over the internet.</span>
            </p>
          )}
          {mode === "tls" && (
            <div className="conn-tls">
              {editing && <p className="hint">Leave the certificate fields blank to keep the stored ones; fill them to replace.</p>}
              <Field label="CA certificate (PEM)" required={!editing} help="Verifies the daemon's identity">
                <textarea required={!editing} rows={2} value={f.tlsCa} onChange={(e) => setF({ ...f, tlsCa: e.target.value })} />
              </Field>
              <div className="row">
                <Field label="Client cert (PEM)" required={!editing}>
                  <textarea required={!editing} rows={2} value={f.tlsCert} onChange={(e) => setF({ ...f, tlsCert: e.target.value })} />
                </Field>
                <Field label="Client key (PEM)" required={!editing}>
                  <textarea required={!editing} rows={2} value={f.tlsKey} onChange={(e) => setF({ ...f, tlsKey: e.target.value })} />
                </Field>
              </div>
            </div>
          )}

          <div className="actions env-actions">
            <span className="muted env-foot">Manages containers, images, volumes & networks on that host.</span>
            <button type="button" onClick={onClose} disabled={busy}>
              Cancel
            </button>
            <button type="submit" className="primary" disabled={busy}>
              {busy ? <Icon.Spinner size={15} /> : editing ? <Icon.Check2 size={15} /> : <Icon.Plus size={15} />}
              <span>{editing ? "Save changes" : "Add environment"}</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

// QuotaPill shows a member's live CPU/RAM usage against their granted quota.
function QuotaPill({ me }: { me: Me }) {
  const cpuPct = me.cpuQuotaMilli ? Math.min(100, (me.cpuUsedMilli / me.cpuQuotaMilli) * 100) : 0;
  const memPct = me.memQuotaMB ? Math.min(100, (me.memUsedMB / me.memQuotaMB) * 100) : 0;
  return (
    <div className="quota-pill" title="Your resource usage vs. granted quota">
      <span className="quota-metric">
        <Icon.Cpu size={13} />
        <span className="quota-bar">
          <span className="quota-fill" style={{ width: `${cpuPct}%` }} />
        </span>
        {(me.cpuUsedMilli / 1000).toFixed(1)}/{(me.cpuQuotaMilli / 1000).toFixed(1)} CPU
      </span>
      <span className="quota-metric">
        <Icon.Box size={13} />
        <span className="quota-bar">
          <span className="quota-fill" style={{ width: `${memPct}%` }} />
        </span>
        {me.memUsedMB}/{me.memQuotaMB} MB
      </span>
    </div>
  );
}

// ===== Overview (home dashboard) =====

function StatTile({
  icon: I,
  label,
  value,
  accent = "accent",
  onClick,
}: {
  icon: (p: { size?: number }) => JSX.Element;
  label: string;
  value: number | undefined;
  accent?: "ok" | "muted" | "accent" | "warn";
  onClick?: () => void;
}) {
  return (
    <button type="button" className={`stat-tile ${accent} ${onClick ? "clickable" : ""}`} onClick={onClick} disabled={!onClick}>
      <span className="stat-icon">
        <I size={18} />
      </span>
      <span className="stat-value">{value === undefined ? <span className="skeleton" style={{ width: 38, height: 22, borderRadius: 6 }} /> : value}</span>
      <span className="stat-label">{label}</span>
    </button>
  );
}

function EnvHealth({ endpoints, onSwitch }: { endpoints: Endpoint[] | null; onSwitch: (id: number) => void }) {
  const [status, setStatus] = useState<Record<number, { ok: boolean; version: string }>>({});
  useEffect(() => {
    endpoints?.forEach((e) => api.endpointStatus(e.id).then((s) => setStatus((p) => ({ ...p, [e.id]: s }))).catch(() => {}));
  }, [endpoints]);
  if (!endpoints) return <Loading />;
  return (
    <ul className="env-health">
      {endpoints.map((e) => {
        const s = status[e.id];
        return (
          <li key={e.id}>
            <button type="button" className="env-health-row" onClick={() => onSwitch(e.id)}>
              <span className={`dot ${dotState(s?.ok)}`} />
              <span className="strong env-h-name">{e.name}</span>
              <span className="muted mono env-h-host">{e.host}</span>
              <span className="muted env-h-ver">{s ? s.version || "unreachable" : "…"}</span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}

function Overview({ me, isAdmin, onGo }: { me: Me | null; isAdmin: boolean; onGo: (t: Tab) => void }) {
  const [containers] = useAsync<Container[]>(() => api.containers(), []);
  const [services] = useAsync<Service[]>(() => api.services(), []);
  // Networks and volumes are member-usable (scoped to what they own); images
  // remain admin-only infrastructure.
  const [images] = useAsync<unknown[]>(() => (isAdmin ? api.images() : Promise.resolve([])), [isAdmin]);
  const [networks] = useAsync<DockerNetwork[]>(() => api.networks(), []);
  const [volumes] = useAsync<DockerVolume[]>(() => api.volumes(), []);
  const [endpoints] = useAsync<Endpoint[]>(() => (isAdmin ? api.endpoints() : Promise.resolve([] as Endpoint[])), [isAdmin]);
  const [pending] = useAsync<ResourceRequest[]>(() => api.requests("pending"), []);

  const running = containers ? containers.filter((c) => c.State === "running").length : undefined;
  const stopped = containers ? containers.length - (running ?? 0) : undefined;
  const num = (a: unknown[] | null) => (a ? a.length : undefined);

  return (
    <div className="overview">
      <div className="stat-grid">
        <StatTile icon={Icon.Play2} label="Running" value={running} accent="ok" onClick={() => onGo("containers")} />
        <StatTile icon={Icon.Stop} label="Stopped" value={stopped} accent="muted" onClick={() => onGo("containers")} />
        <StatTile icon={Icon.Layers} label="Services" value={num(services)} accent="accent" onClick={() => onGo("services")} />
        <StatTile icon={Icon.Network} label="Networks" value={num(networks)} accent="accent" onClick={() => onGo("networks")} />
        <StatTile icon={Icon.Drive} label="Volumes" value={num(volumes)} accent="accent" onClick={() => onGo("volumes")} />
        {isAdmin && <StatTile icon={Icon.Registry} label="Images" value={num(images)} accent="accent" />}
      </div>

      <div className="ov-cols">
        {isAdmin ? (
          <div className="panel">
            <div className="panel-head">
              <Icon.Server size={15} /> Environments
            </div>
            <EnvHealth endpoints={endpoints} onSwitch={(id) => environment.set(id)} />
          </div>
        ) : (
          me && (
            <div className="panel">
              <div className="panel-head">
                <Icon.Cpu size={15} /> Your quota
              </div>
              <div className="ov-quota">
                <QuotaBar label="CPU" used={me.cpuUsedMilli / 1000} total={me.cpuQuotaMilli / 1000} unit="cores" />
                <QuotaBar label="Memory" used={me.memUsedMB} total={me.memQuotaMB} unit="MB" />
              </div>
            </div>
          )
        )}

        <div className="panel">
          <div className="panel-head">
            <Icon.Inbox size={15} /> Needs attention
          </div>
          <div className="attention">
            <button type="button" className="attn-item" onClick={() => onGo("requests")}>
              <span className={`attn-dot ${pending && pending.length > 0 ? "warn" : "ok"}`} />
              <span>
                {pending === null ? "…" : pending.length} pending resource request{pending?.length === 1 ? "" : "s"}
              </span>
              <Icon.Chevron size={14} />
            </button>
            <button type="button" className="attn-item" onClick={() => onGo("containers")}>
              <span className={`attn-dot ${stopped ? "muted" : "ok"}`} />
              <span>
                {stopped === undefined ? "…" : stopped} stopped container{stopped === 1 ? "" : "s"}
              </span>
              <Icon.Chevron size={14} />
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function QuotaBar({ label, used, total, unit }: { label: string; used: number; total: number; unit: string }) {
  const pct = total ? Math.min(100, (used / total) * 100) : 0;
  return (
    <div className="ov-quota-item">
      <div className="ov-quota-head">
        <span className="muted">{label}</span>
        <span className="mono">
          {used.toFixed(unit === "cores" ? 1 : 0)} / {total} {unit}
        </span>
      </div>
      <div className="meter">
        <span className={`meter-fill ${pct >= 90 ? "critical" : pct >= 70 ? "warning" : "ok"}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

function useAsync<T>(fn: () => Promise<T>, deps: unknown[] = []): [T | null, string | null, () => void] {
  const [data, setData] = useState<T | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0);
  useEffect(() => {
    let live = true;
    // The API serializes an empty Go slice as `null`; every useAsync caller is
    // a list, so coerce null/undefined to [] — otherwise an empty result reads
    // as "still loading" and the view spins forever.
    fn()
      .then((d) => live && setData((d ?? []) as T))
      .catch((e) => live && setErr(String(e.message || e)));
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce]);
  return [data, err, () => setNonce((n) => n + 1)];
}

// ActionButton runs one async task with its own spinner + disabled state, and
// reports the outcome through the global toast/activity system.
function ActionButton({
  icon: I,
  label,
  title,
  variant = "default",
  confirm: confirmMsg,
  success,
  task,
  onDone,
}: {
  icon: (p: { size?: number }) => JSX.Element;
  label?: string;
  title?: string;
  variant?: "default" | "primary" | "danger";
  confirm?: string;
  success?: string;
  task: () => Promise<unknown>;
  onDone?: () => void;
}) {
  const [busy, runAction] = useAction();
  const onClick = async () => {
    if (confirmMsg && !window.confirm(confirmMsg)) return;
    await runAction(task(), success ? { success } : undefined);
    onDone?.();
  };
  return (
    <button type="button" className={`btn-icon ${variant}`} title={title || label} disabled={busy} onClick={onClick}>
      {busy ? <Icon.Spinner size={15} /> : <I size={15} />}
      {label && <span>{label}</span>}
    </button>
  );
}

const matchContainer = (c: Container, q: string) => {
  const name = c.Names?.[0]?.replace(/^\//, "").toLowerCase() ?? "";
  return (
    name.includes(q) ||
    c.Image.toLowerCase().includes(q) ||
    c.State.toLowerCase().includes(q) ||
    (c.Labels?.["easydeploy.subdomain"] ?? "").toLowerCase().includes(q)
  );
};

function Containers() {
  const [list, err, reload] = useAsync<Container[]>(() => api.containers(), []);
  const [editing, setEditing] = useState<Container | null>(null);
  const [detailId, setDetailId] = useState<string | null>(null);
  const sp = useSearchPage(list ?? [], matchContainer, 12);

  if (err) return <Err msg={err} onRetry={reload} />;
  if (!list) return <TableSkeleton cols={5} />;

  // The open detail tracks the live container from the list, so lifecycle
  // actions (which reload the list) refresh it, and a removed container closes.
  const detailC = detailId ? list.find((c) => c.Id === detailId) ?? null : null;

  return (
    <>
      <div className="toolbar">
        <ActionButton icon={Icon.Refresh} label="Refresh" task={() => api.containers()} onDone={reload} />
        <SearchInput value={sp.query} onChange={sp.setQuery} placeholder="Search name, image, state…" />
        <span className="count">{sp.filtered.length} of {list.length} container{list.length === 1 ? "" : "s"}</span>
      </div>
      {list.length === 0 ? (
        <Empty icon={Icon.Box} title="No containers yet" hint="Head to Deploy to launch your first one." />
      ) : sp.filtered.length === 0 ? (
        <Empty icon={Icon.Search} title="No matches" hint="No containers match your search." />
      ) : (
        <><div className="table-wrap"><table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Image</th>
              <th>State</th>
              <th>Subdomain</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {sp.pageItems.map((c) => {
              const name = c.Names?.[0]?.replace(/^\//, "");
              return (
                <tr key={c.Id} className="clickable-row" onClick={() => setDetailId(c.Id)} title="View details">
                  <td className="strong">{name}</td>
                  <td className="muted mono">{c.Image}</td>
                  <td>
                    <span className={`badge ${c.State}`}>
                      <span className="badge-dot" />
                      {c.State}
                    </span>
                  </td>
                  <td className="muted">{c.Labels?.["easydeploy.subdomain"] || "—"}</td>
                  <td className="actions" onClick={(e) => e.stopPropagation()}>
                    {c.State === "running" ? (
                      <ActionButton icon={Icon.Stop} title="Stop" success={`Stopped ${name}`} task={() => api.stop(c.Id)} onDone={reload} />
                    ) : (
                      <ActionButton icon={Icon.Play} title="Start" success={`Started ${name}`} task={() => api.start(c.Id)} onDone={reload} />
                    )}
                    <ActionButton icon={Icon.Restart} title="Restart" success={`Restarted ${name}`} task={() => api.restart(c.Id)} onDone={reload} />
                    <ActionButton icon={Icon.Edit} title="Edit configuration" onDone={reload} task={async () => setEditing(c)} />
                    <ActionButton
                      icon={Icon.Update}
                      title="Update image (pull latest & recreate)"
                      confirm={`Pull the latest image and recreate ${name}?`}
                      success={`Updated ${name}`}
                      task={() => api.update(c.Id)}
                      onDone={reload}
                    />
                    <button type="button" className="btn-icon" title="Details (logs, monitor, shell)" onClick={() => setDetailId(c.Id)}>
                      <Icon.Logs size={15} />
                    </button>
                    <ActionButton
                      icon={Icon.Trash}
                      title="Remove"
                      variant="danger"
                      confirm={`Remove ${name}? This cannot be undone.`}
                      success={`Removed ${name}`}
                      task={() => api.remove(c.Id)}
                      onDone={reload}
                    />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table></div>
        <Pager page={sp.page} pageCount={sp.pageCount} onPage={sp.setPage} /></>
      )}
      {detailC && (
        <ContainerDetail
          container={detailC}
          onClose={() => setDetailId(null)}
          onChanged={reload}
          onEdit={() => {
            setEditing(detailC);
            setDetailId(null);
          }}
        />
      )}
      {editing && (
        <ContainerEditor
          container={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      )}
    </>
  );
}

function Loading() {
  return (
    <div className="loading">
      <Icon.Spinner size={22} />
      <span>Loading…</span>
    </div>
  );
}

// useSearchPage filters a list by a text query and paginates the result. It
// returns the current page's items plus the controls to render a search box and
// pager. `match` decides whether an item matches the (already-lowercased) query.
function useSearchPage<T>(items: T[], match: (item: T, q: string) => boolean, pageSize = 12) {
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  const q = query.trim().toLowerCase();
  const filtered = useMemo(() => (q ? items.filter((it) => match(it, q)) : items), [items, q, match]);
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const clamped = Math.min(page, pageCount - 1);
  const pageItems = filtered.slice(clamped * pageSize, clamped * pageSize + pageSize);
  return {
    query,
    setQuery: (v: string) => {
      setQuery(v);
      setPage(0);
    },
    page: clamped,
    setPage,
    pageItems,
    filtered,
    pageCount,
    total: items.length,
  };
}

function SearchInput({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder?: string }) {
  return (
    <div className="search-box">
      <Icon.Search size={15} />
      <input value={value} placeholder={placeholder ?? "Search…"} onChange={(e) => onChange(e.target.value)} />
      {value && (
        <button type="button" className="search-clear" onClick={() => onChange("")} aria-label="Clear search">
          <Icon.Close size={13} />
        </button>
      )}
    </div>
  );
}

// NameCreateModal is a small popup form for resources created from a single
// name (networks, volumes). onCreate returns undefined on failure so the modal
// stays open; anything else closes it.
function NameCreateModal({
  title,
  label,
  placeholder,
  hint,
  onCreate,
  onClose,
}: {
  title: string;
  label: string;
  placeholder: string;
  hint?: string;
  onCreate: (name: string) => Promise<unknown>;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    setBusy(true);
    const ok = await onCreate(name.trim());
    setBusy(false);
    if (ok !== undefined) onClose();
  };
  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body narrow" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong>
            <Icon.Plus size={15} /> {title}
          </strong>
          <button type="button" onClick={onClose} aria-label="Close">
            <Icon.Close size={16} />
          </button>
        </div>
        <form className="form modal-pad" onSubmit={submit}>
          <label>
            {label}
            <input autoFocus value={name} placeholder={placeholder} onChange={(e) => setName(e.target.value)} />
          </label>
          {hint && <p className="hint">{hint}</p>}
          <div className="actions" style={{ justifyContent: "flex-end" }}>
            <button type="button" onClick={onClose} disabled={busy}>
              Cancel
            </button>
            <button type="submit" className="primary" disabled={busy || !name.trim()}>
              {busy ? <Icon.Spinner size={15} /> : <Icon.Plus size={15} />}
              <span>Create</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function Pager({ page, pageCount, onPage }: { page: number; pageCount: number; onPage: (p: number) => void }) {
  if (pageCount <= 1) return null;
  return (
    <div className="pager">
      <button type="button" className="btn-icon" disabled={page <= 0} onClick={() => onPage(page - 1)} aria-label="Previous page">
        <Icon.Back size={14} />
      </button>
      <span className="pager-page">
        Page {page + 1} of {pageCount}
      </span>
      <button type="button" className="btn-icon" disabled={page >= pageCount - 1} onClick={() => onPage(page + 1)} aria-label="Next page">
        <Icon.Back size={14} className="flip-x" />
      </button>
    </div>
  );
}

// Content-shaped placeholders shown while data loads, so a page settles into
// place instead of flashing blank then popping. Prefer these over a spinner
// for list/grid views.
function TableSkeleton({ cols, rows = 6 }: { cols: number; rows?: number }) {
  return (
    <div className="table-wrap">
      <table>
        <tbody>
          {Array.from({ length: rows }).map((_, r) => (
            <tr key={r}>
              {Array.from({ length: cols }).map((_, c) => (
                <td key={c}>
                  <div className="skeleton sk-row" style={{ width: c === 0 ? "55%" : c === cols - 1 ? "36px" : "78%" }} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function CardSkeleton({ count = 4, className = "svc-grid" }: { count?: number; className?: string }) {
  return (
    <div className={className}>
      {Array.from({ length: count }).map((_, i) => (
        <div key={i} className="skeleton sk-card" />
      ))}
    </div>
  );
}

function Empty({
  icon: I,
  title,
  hint,
  action,
}: {
  icon: (p: { size?: number }) => JSX.Element;
  title: string;
  hint?: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="empty">
      <span className="empty-icon">
        <I size={26} />
      </span>
      <p className="empty-title">{title}</p>
      {hint && <p className="muted">{hint}</p>}
      {action && <div className="empty-action">{action}</div>}
    </div>
  );
}

function LogViewer({ container, onClose, embedded }: { container: Container; onClose?: () => void; embedded?: boolean }) {
  const [lines, setLines] = useState<string[]>([]);
  useEffect(() => {
    let closed = false;
    const ws = new WebSocket(wsURL(`/containers/${container.Id}/logs`));
    ws.onmessage = (e) => setLines((prev) => [...prev.slice(-500), e.data as string]);
    ws.onerror = () => {
      if (!closed) setLines((prev) => [...prev, "[log stream error]"]);
    };
    return () => {
      closed = true;
      ws.close();
    };
  }, [container.Id]);
  const body = <pre className="logs">{lines.join("") || "Waiting for log output…"}</pre>;
  if (embedded) return body;
  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong>{container.Names?.[0]}</strong>
          <button onClick={onClose}>Close</button>
        </div>
        {body}
      </div>
    </div>
  );
}

// ContainerDetail is the consolidated view opened by clicking a container: info,
// live logs, monitoring, and an interactive shell in one tabbed modal.
function ContainerDetail({
  container,
  onClose,
  onChanged,
  onEdit,
}: {
  container: Container;
  onClose: () => void;
  onChanged: () => void;
  onEdit: () => void;
}) {
  const name = container.Names?.[0]?.replace(/^\//, "") || container.Id.slice(0, 12);
  const running = container.State === "running";
  const [tab, setTab] = useState<"overview" | "logs" | "monitor" | "shell">("overview");
  const [info, setInfo] = useState<ContainerInspect | null>(null);
  useEffect(() => {
    let live = true;
    api.inspect(container.Id).then((i) => live && setInfo(i)).catch(() => live && setInfo(null));
    return () => {
      live = false;
    };
  }, [container.Id]);

  const tabs = [
    { k: "overview", label: "Overview", icon: Icon.Box, needRun: false },
    { k: "logs", label: "Logs", icon: Icon.Logs, needRun: false },
    { k: "monitor", label: "Monitor", icon: Icon.Activity, needRun: true },
    { k: "shell", label: "Shell", icon: Icon.Terminal, needRun: true },
  ] as const;
  // If the container stops while open, fall back to a tab that still works.
  const activeTab = !running && (tab === "monitor" || tab === "shell") ? "overview" : tab;

  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body wide cdetail" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong>
            <Icon.Box size={15} /> {name}
            <span className={`badge ${container.State}`}>
              <span className="badge-dot" /> {container.State}
            </span>
          </strong>
          <button type="button" onClick={onClose} aria-label="Close">
            <Icon.Close size={16} />
          </button>
        </div>

        <div className="cdetail-actions">
          {running ? (
            <ActionButton icon={Icon.Stop} label="Stop" success={`Stopped ${name}`} task={() => api.stop(container.Id)} onDone={onChanged} />
          ) : (
            <ActionButton icon={Icon.Play} label="Start" success={`Started ${name}`} task={() => api.start(container.Id)} onDone={onChanged} />
          )}
          <ActionButton icon={Icon.Restart} label="Restart" success={`Restarted ${name}`} task={() => api.restart(container.Id)} onDone={onChanged} />
          <ActionButton
            icon={Icon.Update}
            label="Update"
            confirm={`Pull the latest image and recreate ${name}?`}
            success={`Updated ${name}`}
            task={() => api.update(container.Id)}
            onDone={onChanged}
          />
          <button type="button" onClick={onEdit}>
            <Icon.Edit size={14} /> <span>Edit</span>
          </button>
          <ActionButton
            icon={Icon.Trash}
            label="Remove"
            variant="danger"
            confirm={`Remove ${name}? This cannot be undone.`}
            success={`Removed ${name}`}
            task={() => api.remove(container.Id)}
            onDone={onChanged}
          />
        </div>

        <div className="cdetail-tabs">
          {tabs.map((t) => (
            <button
              key={t.k}
              type="button"
              className={activeTab === t.k ? "active" : ""}
              disabled={t.needRun && !running}
              title={t.needRun && !running ? "Container is not running" : undefined}
              onClick={() => setTab(t.k)}
            >
              <t.icon size={14} /> <span>{t.label}</span>
            </button>
          ))}
        </div>

        <div className={`cdetail-body ${activeTab === "shell" ? "is-shell" : ""}`}>
          {activeTab === "overview" && <ContainerOverview c={container} info={info} />}
          {activeTab === "logs" && <LogViewer container={container} embedded />}
          {activeTab === "monitor" && running && <Monitor container={container} embedded />}
          {activeTab === "shell" && running && <Shell container={container} embedded />}
        </div>
      </div>
    </div>
  );
}

function ContainerOverview({ c, info }: { c: Container; info: ContainerInspect | null }) {
  const ports = (c.Ports ?? []).filter((p) => p.PublicPort).map((p) => `${p.PublicPort}→${p.PrivatePort}/${p.Type}`);
  const nets = info ? Object.keys(info.NetworkSettings.Networks ?? {}) : [];
  const env = info?.Config.Env ?? [];
  const labels = Object.entries(info?.Config.Labels ?? {});
  const sub = c.Labels?.["easydeploy.subdomain"];
  const owner = c.Labels?.["easydeploy.owner"];
  const cpu = info?.HostConfig.NanoCpus ? `${(info.HostConfig.NanoCpus / 1e9).toFixed(2)} cores` : "unlimited";
  const mem = info?.HostConfig.Memory ? fmtBytes(info.HostConfig.Memory) : "unlimited";
  const rows: [string, React.ReactNode][] = [
    ["Image", <span className="mono">{c.Image}</span>],
    ["Status", c.Status],
    ["Container ID", <span className="mono">{c.Id.slice(0, 12)}</span>],
    ["Subdomain", sub || "—"],
    ["Owner", owner || "—"],
    ["CPU limit", cpu],
    ["Memory limit", mem],
    ["Networks", nets.join(", ") || "—"],
    ["Published ports", ports.length ? <span className="mono">{ports.join(", ")}</span> : "—"],
  ];
  return (
    <div className="cdetail-overview">
      <dl className="kv">
        {rows.map(([k, v]) => (
          <div key={k}>
            <dt>{k}</dt>
            <dd>{v}</dd>
          </div>
        ))}
      </dl>
      {env.length > 0 && (
        <div className="kv-block">
          <div className="kv-block-title">Environment</div>
          <pre className="mono">{env.join("\n")}</pre>
        </div>
      )}
      {labels.length > 0 && (
        <div className="kv-block">
          <div className="kv-block-title">Labels</div>
          <pre className="mono">{labels.map(([k, v]) => `${k}=${v}`).join("\n")}</pre>
        </div>
      )}
    </div>
  );
}

function ContainerEditor({
  container,
  onClose,
  onSaved,
}: {
  container: Container;
  onClose: () => void;
  onSaved: () => void;
}) {
  const name = container.Names?.[0]?.replace(/^\//, "") || "";
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState<DeployRequest>({
    name,
    image: container.Image,
    env: [],
    subdomain: container.Labels?.["easydeploy.subdomain"] || "",
    containerPort: 0,
    publish: {},
    network: "",
    cpuMilli: 0,
    memMB: 0,
  });
  const [envText, setEnvText] = useState("");

  // Prefill from the live container config.
  useEffect(() => {
    let live = true;
    api
      .inspect(container.Id)
      .then((info) => {
        if (!live) return;
        const env = info.Config.Env || [];
        const firstPort = Object.keys(info.Config.ExposedPorts || {})[0];
        const containerPort = firstPort ? parseInt(firstPort, 10) : 0;
        const network = Object.keys(info.NetworkSettings.Networks || {})[0] || "";
        const cpuMilli = Math.round((info.HostConfig.NanoCpus || 0) / 1_000_000);
        const memMB = Math.round((info.HostConfig.Memory || 0) / (1024 * 1024));
        setForm((f) => ({ ...f, image: info.Config.Image, containerPort, network, cpuMilli, memMB }));
        setEnvText(env.join("\n"));
        setLoaded(true);
      })
      .catch((e) => {
        toast("error", String((e as Error).message || e));
        setLoaded(true);
      });
    return () => {
      live = false;
    };
  }, [container.Id]);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    const env = envText.split("\n").map((l) => l.trim()).filter(Boolean);
    const res = await run(api.edit(container.Id, { ...form, env }), { success: `Saved ${name}` });
    setBusy(false);
    if (res) onSaved();
  };

  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong>
            <Icon.Edit size={15} /> Edit {name}
          </strong>
          <button type="button" onClick={onClose} aria-label="Close">
            <Icon.Close size={16} />
          </button>
        </div>
        {busy && <div className="progress" />}
        {!loaded ? (
          <div className="modal-pad">
            <Loading />
          </div>
        ) : (
          <form className="form modal-pad" onSubmit={save}>
            <label>
              Image
              <input value={form.image} onChange={(e) => setForm({ ...form, image: e.target.value })} />
            </label>
            <div className="row">
              <label>
                Subdomain
                <input value={form.subdomain} onChange={(e) => setForm({ ...form, subdomain: e.target.value })} />
              </label>
              <label>
                Container port
                <input
                  type="number"
                  value={form.containerPort}
                  onChange={(e) => setForm({ ...form, containerPort: Number(e.target.value) })}
                />
              </label>
            </div>
            <div className="row">
              <label>
                CPU (cores)
                <input
                  type="number"
                  step="0.1"
                  min="0"
                  value={form.cpuMilli / 1000}
                  onChange={(e) => setForm({ ...form, cpuMilli: Math.round(Number(e.target.value) * 1000) })}
                />
              </label>
              <label>
                Memory (MB)
                <input
                  type="number"
                  min="0"
                  value={form.memMB}
                  onChange={(e) => setForm({ ...form, memMB: Number(e.target.value) })}
                />
              </label>
            </div>
            <label>
              Environment (KEY=VALUE per line)
              <textarea rows={5} value={envText} onChange={(e) => setEnvText(e.target.value)} />
            </label>
            <label>
              Network
              <input value={form.network} onChange={(e) => setForm({ ...form, network: e.target.value })} />
            </label>
            <div className="actions">
              <button type="submit" className="primary" disabled={busy}>
                {busy ? <Icon.Spinner size={15} /> : <Icon.Check size={15} />}
                <span>{busy ? "Applying…" : "Apply & recreate"}</span>
              </button>
              <button type="button" onClick={onClose} disabled={busy}>
                Cancel
              </button>
            </div>
            <p className="hint">
              Applying recreates the container with the new settings. Its name and data volumes are preserved; the
              container ID changes.
            </p>
          </form>
        )}
      </div>
    </div>
  );
}

// EdgeBanner shows the state of a remote environment's edge Envoy — the data
// plane that makes Routes/Services work on that host. On the local host it
// renders nothing (the local Envoy is always present). Placed atop the Routes
// and Services tabs so a remote deploy has somewhere to publish to.
function EdgeBanner() {
  const [envId, setEnvId] = useState(environment.get());
  const [status, setStatus] = useState<EdgeStatus | null | undefined>(undefined);
  const [busy, act] = useAction();

  useEffect(() => environment.subscribe(() => setEnvId(environment.get())), []);

  const load = () => {
    if (envId === 0) return;
    setStatus(undefined);
    api.edgeStatus(envId).then(setStatus).catch(() => setStatus(null));
  };
  useEffect(load, [envId]);

  if (envId === 0) return null; // local host runs its own Envoy

  const deploy = async () => {
    const ok = await act(api.deployEdge(envId), { success: "Edge proxy deployed" });
    if (ok !== undefined) setStatus(ok);
  };
  const remove = async () => {
    if (!window.confirm("Remove the edge proxy? Routes/services on this host stop being served.")) return;
    const ok = await act(api.removeEdge(envId), { success: "Edge proxy removed" });
    if (ok !== undefined) load();
  };

  const running = status?.present && status.running;
  const tone = running ? "ok" : status === undefined ? "" : "warn";

  return (
    <div className={`edge-banner ${tone}`}>
      <span className="edge-ico">
        <Icon.Globe size={16} />
      </span>
      <div className="edge-text">
        {status === undefined ? (
          <span className="strong">Checking edge proxy…</span>
        ) : running ? (
          <>
            <span className="strong">Edge proxy running</span>
            <span className="muted">Serving subdomains on this host{status.hostPort ? ` · port ${status.hostPort}` : ""}.</span>
          </>
        ) : (
          <>
            <span className="strong">No edge proxy on this host yet</span>
            <span className="muted">Deploy an Envoy here so routes and services on this environment are served.</span>
          </>
        )}
      </div>
      <div className="edge-acts">
        {status !== undefined && (
          <>
            <button type="button" className={running ? "" : "primary"} onClick={deploy} disabled={busy}>
              {busy ? <Icon.Spinner size={14} /> : <Icon.Rocket size={14} />}
              <span>{running ? "Redeploy" : status?.present ? "Restart" : "Deploy edge proxy"}</span>
            </button>
            {status?.present && (
              <button type="button" className="danger" onClick={remove} disabled={busy}>
                <Icon.Trash size={14} /> <span>Remove</span>
              </button>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function Routes() {
  const [list, err, reload] = useAsync<Route[]>(() => api.routes(), []);
  const del = async (s: string) => {
    try {
      await api.deleteRoute(s);
      reload();
    } catch (e) {
      toast("error", String(e));
    }
  };
  if (err) return <Err msg={err} onRetry={reload} />;
  if (!list) return (<><EdgeBanner /><TableSkeleton cols={3} /></>);
  return (
    <>
    <EdgeBanner />
    <div className="table-wrap"><table>
      <thead>
        <tr>
          <th>Subdomain</th>
          <th>Target</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {list.length === 0 && (
          <tr>
            <td colSpan={3} className="muted">
              No routes yet. Deploy a container with a subdomain.
            </td>
          </tr>
        )}
        {list.map((r) => (
          <tr key={r.id}>
            <td>{r.subdomain}</td>
            <td className="muted">
              {r.targetHost}:{r.targetPort}
            </td>
            <td className="actions">
              <button className="danger" onClick={() => del(r.subdomain)}>
                Delete
              </button>
            </td>
          </tr>
        ))}
      </tbody>
    </table></div>
    </>
  );
}

// ExposeDiagram is a GCP-style architecture picture: internet traffic enters
// through one of the two exposure paths, hits the Envoy load balancer, and is
// fanned out (routed by subdomain, round-robined) to the replica containers.
// `active` softly highlights the path for the option the user is hovering.
function ExposeDiagram({ active }: { active: "ip" | "tunnel" | null }) {
  const ipOn = active !== "tunnel";
  const tunOn = active !== "ip";
  return (
    <svg className="expose-svg" viewBox="0 0 980 340" role="img" preserveAspectRatio="xMidYMid meet"
      aria-label="Internet traffic flows through a public entry point to the Envoy load balancer, which routes by subdomain to your replica containers.">
      <defs>
        <marker id="exp-ar" markerWidth="9" markerHeight="9" refX="7" refY="3" orient="auto" markerUnits="strokeWidth">
          <path d="M0,0 L7,3 L0,6 Z" fill="var(--muted)" />
        </marker>
        <marker id="exp-ar-a" markerWidth="9" markerHeight="9" refX="7" refY="3" orient="auto" markerUnits="strokeWidth">
          <path d="M0,0 L7,3 L0,6 Z" fill="var(--accent)" />
        </marker>
      </defs>

      {/* flow arrows (drawn first so nodes sit on top) */}
      <g fill="none" strokeWidth="2">
        {/* internet -> entry points */}
        <path d="M150 150 C 190 120, 200 104, 232 104" stroke="var(--border-strong)" markerEnd="url(#exp-ar)" opacity={ipOn ? 1 : 0.25} />
        <path d="M150 190 C 190 220, 200 236, 232 236" stroke="var(--border-strong)" markerEnd="url(#exp-ar)" opacity={tunOn ? 1 : 0.25} />
        {/* entry points -> envoy */}
        <path d="M432 104 C 480 104, 486 150, 520 158" stroke={ipOn ? "var(--accent)" : "var(--border-strong)"} markerEnd={ipOn ? "url(#exp-ar-a)" : "url(#exp-ar)"} opacity={ipOn ? 1 : 0.25} />
        <path d="M432 236 C 480 236, 486 190, 520 182" stroke={tunOn ? "var(--accent)" : "var(--border-strong)"} markerEnd={tunOn ? "url(#exp-ar-a)" : "url(#exp-ar)"} opacity={tunOn ? 1 : 0.25} />
        {/* envoy -> replicas */}
        <path d="M690 150 C 730 120, 740 96, 772 90" stroke="var(--accent)" markerEnd="url(#exp-ar-a)" />
        <path d="M690 170 L 772 170" stroke="var(--accent)" markerEnd="url(#exp-ar-a)" />
        <path d="M690 190 C 730 220, 740 244, 772 250" stroke="var(--accent)" markerEnd="url(#exp-ar-a)" />
      </g>

      {/* Internet */}
      <g>
        <rect x="16" y="126" width="130" height="88" rx="12" fill="var(--panel-2)" stroke="var(--border)" />
        <circle cx="52" cy="164" r="14" fill="none" stroke="var(--muted)" strokeWidth="1.6" />
        <path d="M38 164h28M52 150c4 4 6 9 6 14s-2 10-6 14c-4-4-6-9-6-14s2-10 6-14Z" fill="none" stroke="var(--muted)" strokeWidth="1.6" />
        <text x="81" y="160" className="d-t">Internet</text>
        <text x="81" y="178" className="d-s">your users</text>
        <text x="81" y="196" className="d-s">:80 / :443</text>
      </g>

      {/* Option 1 — Public IP */}
      <g opacity={ipOn ? 1 : 0.4}>
        <rect x="232" y="72" width="200" height="64" rx="12" fill="var(--panel-2)" stroke={ipOn ? "var(--accent)" : "var(--border)"} />
        <text x="252" y="100" className="d-t">WiFi / NAT public IP</text>
        <text x="252" y="119" className="d-s">router forwards port 80 → here</text>
      </g>

      {/* Option 2 — Cloud VM */}
      <g opacity={tunOn ? 1 : 0.4}>
        <rect x="232" y="204" width="200" height="64" rx="12" fill="var(--panel-2)" stroke={tunOn ? "var(--accent)" : "var(--border)"} />
        <text x="252" y="230" className="d-t">Cloud VM</text>
        <text x="252" y="249" className="d-s">SSH reverse tunnel · no router</text>
        <text x="252" y="262" className="d-s">public IP, always reachable</text>
      </g>

      {/* Envoy load balancer */}
      <g>
        <rect x="520" y="112" width="170" height="116" rx="14" fill="color-mix(in srgb, var(--accent) 12%, var(--panel))" stroke="var(--accent)" strokeWidth="1.6" />
        <text x="605" y="146" className="d-t d-c" fill="var(--accent-strong)">Envoy</text>
        <text x="605" y="166" className="d-s d-c">load balancer</text>
        <text x="605" y="188" className="d-s d-c">routes by subdomain</text>
        <text x="605" y="204" className="d-s d-c">app.you.com → cluster</text>
        <text x="605" y="220" className="d-s d-c">round-robin ↻</text>
      </g>

      {/* Replica containers */}
      {[
        { y: 62, i: 0 },
        { y: 146, i: 1 },
        { y: 230, i: 2 },
      ].map(({ y, i }) => (
        <g key={i}>
          <rect x="772" y={y} width="192" height="56" rx="11" fill="var(--panel-2)" stroke="var(--border)" />
          <rect x="788" y={y + 18} width="20" height="20" rx="4" fill="none" stroke="var(--ok)" strokeWidth="1.6" />
          <text x="820" y={y + 27} className="d-t">app-{i}</text>
          <text x="820" y={y + 44} className="d-s">replica container</text>
        </g>
      ))}
      <text x="868" y="322" className="d-s d-c">N replicas · scaled by the service</text>
    </svg>
  );
}

function Expose() {
  const [ip, setIp] = useState<string>("");
  const [detecting, setDetecting] = useState(false);
  const [hover, setHover] = useState<"ip" | "tunnel" | null>(null);
  const [tunnels, err, reload] = useAsync<Tunnel[]>(() => api.tunnels(), []);
  const [form, setForm] = useState({ name: "", sshHost: "", sshPort: 22, sshUser: "root", remotePort: 80, localPort: 10000 });

  const detect = () => {
    setDetecting(true);
    api.publicIP().then((r) => setIp(r.ip)).catch((e) => toast("error", String(e))).finally(() => setDetecting(false));
  };

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await api.createTunnel({ kind: "ssh", ...form });
      setForm({ name: "", sshHost: "", sshPort: 22, sshUser: "root", remotePort: 80, localPort: 10000 });
      reload();
    } catch (err) {
      toast("error", String(err));
    }
  };

  return (
    <div className="expose">
      <div className="panel expose-hero">
        <div className="expose-hero-head">
          <h3>How exposure works</h3>
          <p className="muted">
            EasyDeploy already routes each service to a subdomain through its built-in Envoy load balancer.
            To make those subdomains reachable from the public internet, point traffic at Envoy one of two ways below.
          </p>
        </div>
        <ExposeDiagram active={hover} />
      </div>

      <div className="expose-grid">
        {/* Option 1 */}
        <div className="panel expose-card" onMouseEnter={() => setHover("ip")} onMouseLeave={() => setHover(null)}>
          <div className="expose-card-head">
            <span className="expose-num">1</span>
            <div>
              <h4>WiFi / NAT public IP</h4>
              <p className="muted">Best when this machine has a routable IP or you can port-forward on your router.</p>
            </div>
          </div>
          <ol className="expose-steps">
            <li>Detect your current public IP.</li>
            <li>On your router, forward external port <code>80</code> (and <code>443</code>) to this machine.</li>
            <li>Point your domain's DNS at that IP.</li>
          </ol>
          <button className="primary" onClick={detect} disabled={detecting}>
            {detecting ? <Icon.Spinner /> : <Icon.Globe />}
            <span>{detecting ? "Detecting…" : "Detect public IP"}</span>
          </button>
          {ip && (
            <div className="expose-result">
              <span className="muted">Public IP</span>
              <code>{ip}</code>
              <span className="muted">— forward port 80 on your router to this machine.</span>
            </div>
          )}
        </div>

        {/* Option 2 */}
        <div className="panel expose-card" onMouseEnter={() => setHover("tunnel")} onMouseLeave={() => setHover(null)}>
          <div className="expose-card-head">
            <span className="expose-num">2</span>
            <div>
              <h4>Cloud VM SSH reverse tunnel</h4>
              <p className="muted">Best behind NAT/CGNAT — a cloud VM's public IP forwards to your local Envoy, no router config.</p>
            </div>
          </div>
          <form className="form" onSubmit={create}>
            <div className="row">
              <label>
                Name
                <input value={form.name} placeholder="prod-tunnel" onChange={(e) => setForm({ ...form, name: e.target.value })} />
              </label>
              <label>
                SSH host
                <input value={form.sshHost} placeholder="vm.example.com" onChange={(e) => setForm({ ...form, sshHost: e.target.value })} />
              </label>
            </div>
            <div className="row">
              <label>
                SSH user
                <input value={form.sshUser} onChange={(e) => setForm({ ...form, sshUser: e.target.value })} />
              </label>
              <label>
                SSH port
                <input type="number" value={form.sshPort} onChange={(e) => setForm({ ...form, sshPort: Number(e.target.value) })} />
              </label>
            </div>
            <div className="row">
              <label>
                Remote port <span className="muted">(on VM)</span>
                <input type="number" value={form.remotePort} onChange={(e) => setForm({ ...form, remotePort: Number(e.target.value) })} />
              </label>
              <label>
                Local port <span className="muted">(Envoy)</span>
                <input type="number" value={form.localPort} onChange={(e) => setForm({ ...form, localPort: Number(e.target.value) })} />
              </label>
            </div>
            <button type="submit" className="primary">
              <Icon.Plus />
              <span>Add tunnel</span>
            </button>
          </form>
        </div>
      </div>

      {/* Tunnels list */}
      <div className="panel expose-tunnels">
        <div className="panel-head">
          <h3>SSH tunnels</h3>
        </div>
        {err && <Err msg={err} onRetry={reload} />}
        {!err && tunnels && tunnels.length === 0 ? (
          <Empty icon={Icon.Route} title="No tunnels yet" hint="Add a Cloud VM tunnel above to expose Envoy through a VM's public IP." />
        ) : (
          <div className="table-wrap"><table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Target</th>
                <th>Status</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {tunnels?.map((t) => (
                <tr key={t.id}>
                  <td className="strong">{t.name}</td>
                  <td className="muted mono">
                    {t.sshUser}@{t.sshHost}:{t.remotePort} → :{t.localPort}
                  </td>
                  <td>
                    <span className={`badge ${t.running ? "running" : "exited"}`}>
                      {t.running ? "running" : "stopped"}
                    </span>
                  </td>
                  <td className="actions">
                    {t.running ? (
                      <button onClick={() => api.stopTunnel(t.id).then(reload)}>Stop</button>
                    ) : (
                      <button onClick={() => api.startTunnel(t.id).then(reload).catch((e) => toast("error", String(e)))}>
                        Start
                      </button>
                    )}
                    <button className="danger" onClick={() => api.deleteTunnel(t.id).then(reload)}>
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table></div>
        )}
      </div>
    </div>
  );
}

const matchNetwork = (n: DockerNetwork, q: string) =>
  n.Name.toLowerCase().includes(q) || n.Driver.toLowerCase().includes(q) || (n.Scope ?? "").toLowerCase().includes(q);

function Networks() {
  const [list, err, reload] = useAsync<DockerNetwork[]>(() => api.networks(), []);
  const [creating, setCreating] = useState(false);
  const sp = useSearchPage(list ?? [], matchNetwork, 12);

  const del = async (id: string) => {
    try {
      await api.removeNetwork(id);
      reload();
    } catch (e) {
      toast("error", String(e));
    }
  };

  if (err) return <Err msg={err} onRetry={reload} />;
  if (!list) return <TableSkeleton cols={5} />;
  return (
    <>
      <div className="toolbar">
        <button type="button" className="primary" onClick={() => setCreating(true)}>
          <Icon.Plus size={15} /> <span>New network</span>
        </button>
        {list.length > 0 && <SearchInput value={sp.query} onChange={sp.setQuery} placeholder="Search networks…" />}
        <span className="count">{sp.filtered.length} of {list.length} network{list.length === 1 ? "" : "s"}</span>
      </div>
      {creating && (
        <NameCreateModal
          title="New network"
          label="Network name"
          placeholder="my-network"
          hint="Created with the bridge driver on this host."
          onCreate={(n) => run(api.createNetwork(n), { success: `Created network ${n}` }).then((r) => (r !== undefined ? (reload(), r) : undefined))}
          onClose={() => setCreating(false)}
        />
      )}
      <div className="table-wrap"><table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Driver</th>
            <th>Scope</th>
            <th>Containers</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {sp.pageItems.map((n) => (
            <tr key={n.Id}>
              <td>{n.Name}</td>
              <td className="muted">{n.Driver}</td>
              <td className="muted">{n.Scope}</td>
              <td className="muted">{n.Containers ? Object.keys(n.Containers).length : 0}</td>
              <td className="actions">
                {!["bridge", "host", "none"].includes(n.Name) && (
                  <button className="danger" onClick={() => del(n.Id)}>
                    Delete
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table></div>
      <Pager page={sp.page} pageCount={sp.pageCount} onPage={sp.setPage} />
    </>
  );
}

// registryKind maps a host to a recognizable provider label so cards read at a
// glance instead of showing a bare hostname.
function registryKind(url: string): string {
  const h = url.replace(/^https?:\/\//, "").replace(/\/$/, "").toLowerCase();
  if (/(^|\.)docker\.io$/.test(h) || h === "registry-1.docker.io" || h === "index.docker.io") return "Docker Hub";
  if (h === "ghcr.io") return "GitHub";
  if (h.endsWith("gitlab.com") || h.includes("gitlab")) return "GitLab";
  if (h === "quay.io") return "Quay";
  if (h.endsWith(".azurecr.io")) return "Azure";
  if (h.endsWith(".pkg.dev") || h.endsWith("gcr.io")) return "Google";
  if (h.includes("amazonaws.com")) return "Amazon ECR";
  return "Private";
}

const matchRegistry = (r: Registry, q: string) =>
  r.name.toLowerCase().includes(q) || r.url.toLowerCase().includes(q) || (r.username ?? "").toLowerCase().includes(q);

function Registries() {
  const [list, err, reload] = useAsync<Registry[]>(() => api.registries(), []);
  const [creating, setCreating] = useState(false);
  const [browse, setBrowse] = useState<Registry | null>(null);
  const sp = useSearchPage(list ?? [], matchRegistry, 9);

  if (err) return <Err msg={err} onRetry={reload} />;
  if (!list) return <CardSkeleton />;

  return (
    <>
      <div className="toolbar">
        <button type="button" className="primary" onClick={() => setCreating(true)}>
          <Icon.Plus size={15} /> <span>Add registry</span>
        </button>
        {list.length > 0 && <SearchInput value={sp.query} onChange={sp.setQuery} placeholder="Search registries…" />}
        <span className="count">{sp.filtered.length} of {list.length} registr{list.length === 1 ? "y" : "ies"}</span>
      </div>

      {list.length === 0 ? (
        <Empty
          icon={Icon.Registry}
          title="No registries yet"
          hint="Add a private registry so pulls authenticate automatically — credentials are encrypted at rest."
          action={<button type="button" className="primary" onClick={() => setCreating(true)}><Icon.Plus size={15} /> <span>Add registry</span></button>}
        />
      ) : sp.filtered.length === 0 ? (
        <Empty icon={Icon.Search} title="No matches" hint="No registries match your search." />
      ) : (
        <>
          <div className="reg-grid">
            {sp.pageItems.map((r) => (
              <RegistryCard key={r.id} reg={r} onBrowse={() => setBrowse(r)} onChanged={reload} />
            ))}
          </div>
          <Pager page={sp.page} pageCount={sp.pageCount} onPage={sp.setPage} />
        </>
      )}

      {creating && <RegistryCreateModal onClose={() => setCreating(false)} onCreated={() => { setCreating(false); reload(); }} />}
      {browse && <RegistryCatalogModal reg={browse} onClose={() => setBrowse(null)} />}
    </>
  );
}

function RegistryCard({ reg, onBrowse, onChanged }: { reg: Registry; onBrowse: () => void; onChanged: () => void }) {
  return (
    <div className="reg-card">
      <div className="reg-head">
        <span className="reg-icon"><Icon.Registry size={18} /></span>
        <div className="reg-title">
          <strong>{reg.name}</strong>
          <span className="mono muted reg-host">{reg.url}</span>
        </div>
        <span className="reg-kind">{registryKind(reg.url)}</span>
      </div>
      <div className="reg-body">
        <div className="reg-field">
          <span className="reg-label">User</span>
          <span>{reg.username || <span className="muted">anonymous</span>}</span>
        </div>
        <div className="reg-field">
          <span className="reg-label">Password</span>
          <span className="muted"><Icon.Key size={12} /> encrypted</span>
        </div>
      </div>
      <div className="reg-actions">
        <button type="button" onClick={onBrowse}>
          <Icon.Search size={14} /> <span>Browse repos</span>
        </button>
        <ActionButton
          icon={Icon.Trash}
          label="Delete"
          variant="danger"
          confirm={`Delete registry ${reg.name}?`}
          success={`Deleted ${reg.name}`}
          task={() => api.deleteRegistry(reg.id)}
          onDone={onChanged}
        />
      </div>
    </div>
  );
}

function RegistryCreateModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [f, setF] = useState({ name: "", url: "", username: "", password: "" });
  const [busy, setBusy] = useState(false);
  const [test, setTest] = useState<{ state: "idle" | "testing" | "ok" | "fail"; msg?: string }>({ state: "idle" });
  const valid = f.name.trim() !== "" && f.url.trim() !== "";

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!valid) return;
    setBusy(true);
    const res = await run(api.createRegistry(f), { success: `Added ${f.name}` });
    setBusy(false);
    if (res !== undefined) onCreated();
  };
  const testLogin = async () => {
    setTest({ state: "testing" });
    try {
      await api.testRegistry({ url: f.url, username: f.username, password: f.password });
      setTest({ state: "ok", msg: "Login succeeded" });
    } catch (e) {
      setTest({ state: "fail", msg: String((e as Error).message || e) });
    }
  };

  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body narrow" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong><Icon.Registry size={15} /> Add registry</strong>
          <button type="button" onClick={onClose} aria-label="Close"><Icon.Close size={16} /></button>
        </div>
        <form className="form modal-pad" onSubmit={submit}>
          <div className="row">
            <label>
              Name
              <input autoFocus value={f.name} placeholder="my-ghcr" onChange={(e) => { setF({ ...f, name: e.target.value }); setTest({ state: "idle" }); }} />
            </label>
            <label>
              Host
              <input value={f.url} placeholder="ghcr.io" onChange={(e) => { setF({ ...f, url: e.target.value }); setTest({ state: "idle" }); }} />
            </label>
          </div>
          <div className="row">
            <label>
              Username
              <input value={f.username} placeholder="optional for public" onChange={(e) => { setF({ ...f, username: e.target.value }); setTest({ state: "idle" }); }} />
            </label>
            <label>
              Password / token
              <input type="password" value={f.password} onChange={(e) => { setF({ ...f, password: e.target.value }); setTest({ state: "idle" }); }} />
            </label>
          </div>
          <p className="hint"><Icon.Key size={12} /> Encrypted with AES-256-GCM before storage — never returned by the API.</p>
          {test.state !== "idle" && (
            <div className={`reg-test reg-test-${test.state}`}>
              {test.state === "testing" ? <Icon.Spinner size={14} /> : test.state === "ok" ? <Icon.Check2 size={14} /> : <Icon.Alert size={14} />}
              <span>{test.state === "testing" ? "Testing login…" : test.msg}</span>
            </div>
          )}
          <div className="actions" style={{ justifyContent: "space-between" }}>
            <button type="button" onClick={testLogin} disabled={busy || test.state === "testing" || !f.url.trim()}>
              Test login
            </button>
            <div className="actions">
              <button type="button" onClick={onClose} disabled={busy}>Cancel</button>
              <button type="submit" className="primary" disabled={busy || !valid}>
                {busy ? <Icon.Spinner size={15} /> : <Icon.Plus size={15} />}
                <span>Add registry</span>
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  );
}

// RegistryCatalogModal browses a registry: it lists repositories, and clicking
// one loads its tags inline.
function RegistryCatalogModal({ reg, onClose }: { reg: Registry; onClose: () => void }) {
  const [repos, err, reload] = useAsync<string[]>(() => api.registryCatalog(reg.id).then((r) => r.repositories ?? []), []);
  const [query, setQuery] = useState("");
  const [tags, setTags] = useState<Record<string, string[] | "loading">>({});

  const toggle = async (repo: string) => {
    if (tags[repo]) {
      setTags((t) => { const n = { ...t }; delete n[repo]; return n; });
      return;
    }
    setTags((t) => ({ ...t, [repo]: "loading" }));
    try {
      const { tags: ts } = await api.registryTags(reg.id, repo);
      setTags((t) => ({ ...t, [repo]: ts ?? [] }));
    } catch (e) {
      toast("error", String(e));
      setTags((t) => { const n = { ...t }; delete n[repo]; return n; });
    }
  };

  const shown = (repos ?? []).filter((r) => r.toLowerCase().includes(query.toLowerCase()));

  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong><Icon.Registry size={15} /> {reg.name} · repositories</strong>
          <button type="button" onClick={onClose} aria-label="Close"><Icon.Close size={16} /></button>
        </div>
        <div className="modal-pad">
          {err ? (
            <Err msg={err} onRetry={reload} />
          ) : !repos ? (
            <Loading />
          ) : repos.length === 0 ? (
            <Empty icon={Icon.Box} title="No repositories" hint="This registry is empty or does not support the _catalog API." />
          ) : (
            <>
              {repos.length > 6 && (
                <div className="reg-cat-search">
                  <SearchInput value={query} onChange={setQuery} placeholder="Filter repositories…" />
                </div>
              )}
              <ul className="reg-repos">
                {shown.map((repo) => {
                  const t = tags[repo];
                  return (
                    <li key={repo} className="reg-repo">
                      <button type="button" className="reg-repo-head" onClick={() => toggle(repo)}>
                        <Icon.Chevron size={14} className={t ? "chev-open" : ""} />
                        <Icon.Box size={14} />
                        <span className="mono">{repo}</span>
                      </button>
                      {t === "loading" && <div className="reg-tags muted"><Icon.Spinner size={13} /> loading tags…</div>}
                      {Array.isArray(t) && (
                        <div className="reg-tags">
                          {t.length === 0 ? (
                            <span className="muted">no tags</span>
                          ) : (
                            t.map((tag) => <span key={tag} className="tag-chip mono">{tag}</span>)
                          )}
                        </div>
                      )}
                    </li>
                  );
                })}
                {shown.length === 0 && <p className="muted">No repositories match “{query}”.</p>}
              </ul>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

// Environments is the dedicated multi-host management view: every Docker host
// with its live status, connection type, and (for remotes) the edge proxy.
function connType(e: Endpoint): string {
  if (e.local) return "Local socket";
  if (e.host.startsWith("ssh://")) return "SSH tunnel";
  return e.tls ? "TCP + TLS" : "Plain TCP";
}

function Environments() {
  const [list, err, reload] = useAsync<Endpoint[]>(() => api.endpoints(), []);
  const [status, setStatus] = useState<Record<number, { ok: boolean; version: string }>>({});
  const [edges, setEdges] = useState<Record<number, EdgeStatus>>({});
  const [adding, setAdding] = useState(false);
  const [editingEnv, setEditingEnv] = useState<Endpoint | null>(null);
  const [curId, setCurId] = useState(environment.get());

  useEffect(() => environment.subscribe(() => setCurId(environment.get())), []);
  useEffect(() => {
    if (!list) return;
    list.forEach((e) => {
      api
        .endpointStatus(e.id)
        .then((s) => {
          setStatus((p) => ({ ...p, [e.id]: s }));
          // Only probe the edge proxy on a reachable remote — otherwise the
          // (unbounded) inspect over SSH hangs on a dead host.
          if (!e.local && s.ok) {
            api.edgeStatus(e.id).then((es) => setEdges((p) => ({ ...p, [e.id]: es }))).catch(() => {});
          }
        })
        .catch(() => {});
    });
  }, [list]);

  const refreshEdge = (id: number) =>
    api.edgeStatus(id).then((s) => setEdges((p) => ({ ...p, [id]: s }))).catch(() => {});

  if (err) return <Err msg={err} onRetry={reload} />;
  if (!list) return <CardSkeleton count={2} className="env-grid" />;

  return (
    <>
      <div className="toolbar">
        <button type="button" className="primary" onClick={() => setAdding(true)}>
          <Icon.Plus size={15} /> <span>New environment</span>
        </button>
        <span className="count">{list.length} environment{list.length === 1 ? "" : "s"}</span>
      </div>
      <div className="env-grid">
        {list.map((e) => {
          const st = status[e.id];
          const edge = edges[e.id];
          const active = e.id === curId;
          const edgeText = edge
            ? edge.running
              ? `running · port ${edge.hostPort}`
              : edge.present
                ? "stopped"
                : "not deployed"
            : "checking…";
          return (
            <div key={e.id} className={`env-card ${active ? "active" : ""}`}>
              <div className="env-card-head">
                <span className="env-card-name">
                  <Icon.Server size={15} /> {e.name}
                  {active && <span className="dom-tag">current</span>}
                </span>
                <span className={`badge ${st?.ok ? "running" : st === undefined ? "" : "exited"}`}>
                  <span className="badge-dot" /> {st === undefined ? "checking" : st.ok ? "online" : "offline"}
                </span>
              </div>
              <div className="env-card-meta">
                <span className="muted mono env-card-host" title={e.host}>{e.host}</span>
                <span className="muted">
                  {connType(e)}
                  {st?.version ? ` · Docker ${st.version}` : ""}
                </span>
              </div>

              {!e.local && (
                <div className="env-edge">
                  <span className="muted env-edge-status">
                    <Icon.Globe size={13} /> Edge proxy: {edgeText}
                  </span>
                  <div className="actions">
                    <ActionButton
                      icon={Icon.Rocket}
                      label={edge?.present ? "Redeploy" : "Deploy"}
                      title="Deploy the edge Envoy so routes/services work on this host"
                      success="Edge proxy deployed"
                      task={() => api.deployEdge(e.id)}
                      onDone={() => refreshEdge(e.id)}
                    />
                    {edge?.present && (
                      <ActionButton
                        icon={Icon.Trash}
                        label="Remove edge"
                        variant="danger"
                        confirm="Remove the edge proxy? Routes/services on this host stop being served."
                        success="Edge proxy removed"
                        task={() => api.removeEdge(e.id)}
                        onDone={() => refreshEdge(e.id)}
                      />
                    )}
                  </div>
                </div>
              )}

              <div className="env-card-actions">
                {active ? (
                  <span className="muted">
                    <Icon.Check size={14} /> Selected
                  </span>
                ) : (
                  <button type="button" onClick={() => environment.set(e.id)}>
                    <Icon.Server size={14} /> <span>Switch to</span>
                  </button>
                )}
                {!e.local && (
                  <span className="actions" style={{ marginLeft: "auto" }}>
                    <button type="button" onClick={() => setEditingEnv(e)}>
                      <Icon.Edit size={14} /> <span>Edit</span>
                    </button>
                    <ActionButton
                      icon={Icon.Trash}
                      label="Remove"
                      variant="danger"
                      confirm={`Remove environment ${e.name}?`}
                      success={`Removed ${e.name}`}
                      task={() => api.deleteEndpoint(e.id)}
                      onDone={() => {
                        if (curId === e.id) environment.set(0);
                        reload();
                      }}
                    />
                  </span>
                )}
              </div>
            </div>
          );
        })}
      </div>
      {(adding || editingEnv) && (
        <EnvModal
          editing={editingEnv ?? undefined}
          onClose={() => {
            setAdding(false);
            setEditingEnv(null);
          }}
          onAdded={() => {
            setAdding(false);
            setEditingEnv(null);
            reload();
          }}
        />
      )}
    </>
  );
}

const matchUser = (u: User, q: string) => u.username.toLowerCase().includes(q) || u.role.toLowerCase().includes(q);

function Users() {
  const [list, err, reload] = useAsync<User[]>(() => api.users(), []);
  const [creating, setCreating] = useState(false);
  const sp = useSearchPage(list ?? [], matchUser, 12);

  if (err) return <Err msg={err} onRetry={reload} />;
  if (!list) return <TableSkeleton cols={4} />;

  return (
    <>
      <div className="toolbar">
        <button type="button" className="primary" onClick={() => setCreating(true)}>
          <Icon.Plus size={15} /> <span>New user</span>
        </button>
        {list.length > 0 && <SearchInput value={sp.query} onChange={sp.setQuery} placeholder="Search users…" />}
        <span className="count">{sp.filtered.length} of {list.length} user{list.length === 1 ? "" : "s"}</span>
      </div>
      {sp.filtered.length === 0 ? (
        <Empty icon={Icon.Search} title="No matches" hint="No users match your search." />
      ) : (
        <>
          <div className="table-wrap"><table>
            <thead>
              <tr>
                <th>User</th>
                <th>Role</th>
                <th>Local quota</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {sp.pageItems.map((u) => (
                <UserRow key={u.id} user={u} onChanged={reload} />
              ))}
            </tbody>
          </table></div>
          <Pager page={sp.page} pageCount={sp.pageCount} onPage={sp.setPage} />
        </>
      )}
      {creating && <UserCreateModal onClose={() => setCreating(false)} onCreated={() => { setCreating(false); reload(); }} />}
    </>
  );
}

function UserCreateModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [f, setF] = useState({ username: "", password: "", role: "member" as Role, cpuCores: 1, memMB: 512 });
  const [busy, setBusy] = useState(false);
  const valid = f.username.trim() !== "" && f.password.length >= 6;
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!valid) return;
    setBusy(true);
    const res = await run(
      api.createUser({
        username: f.username.trim(),
        password: f.password,
        role: f.role,
        cpuQuotaMilli: Math.round(f.cpuCores * 1000),
        memQuotaMB: f.memMB,
      }),
      { success: `Created ${f.username}` },
    );
    setBusy(false);
    if (res) onCreated();
  };
  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body narrow" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong>
            <Icon.Users size={15} /> New user
          </strong>
          <button type="button" onClick={onClose} aria-label="Close">
            <Icon.Close size={16} />
          </button>
        </div>
        <form className="form modal-pad" onSubmit={submit}>
          <label>
            Username
            <input autoFocus value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} />
          </label>
          <label>
            Password
            <input type="password" value={f.password} placeholder="at least 6 characters" onChange={(e) => setF({ ...f, password: e.target.value })} />
          </label>
          <label>
            Role
            <select value={f.role} onChange={(e) => setF({ ...f, role: e.target.value as Role })}>
              <option value="member">member</option>
              <option value="admin">admin</option>
            </select>
          </label>
          {f.role === "member" && (
            <div className="row">
              <label>
                CPU quota (cores)
                <input type="number" step="0.1" min="0" value={f.cpuCores} onChange={(e) => setF({ ...f, cpuCores: Number(e.target.value) })} />
              </label>
              <label>
                Memory quota (MB)
                <input type="number" min="0" value={f.memMB} onChange={(e) => setF({ ...f, memMB: Number(e.target.value) })} />
              </label>
            </div>
          )}
          <div className="actions" style={{ justifyContent: "flex-end" }}>
            <button type="button" onClick={onClose} disabled={busy}>
              Cancel
            </button>
            <button type="submit" className="primary" disabled={busy || !valid}>
              {busy ? <Icon.Spinner size={15} /> : <Icon.Plus size={15} />}
              <span>Create user</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function UserRow({ user, onChanged }: { user: User; onChanged: () => void }) {
  const isAdmin = user.role === "admin";
  const [manage, setManage] = useState(false);
  return (
    <tr>
      <td className="strong">{user.username}</td>
      <td>
        <span className={`role-badge ${isAdmin ? "role-admin" : "role-member"}`}>{user.role}</span>
      </td>
      <td className="muted">
        {isAdmin ? (
          "unlimited"
        ) : (
          <>
            {formatCores(user.cpuQuotaMilli)} cores <span className="dot-sep">·</span> {user.memQuotaMB} MB
          </>
        )}
      </td>
      <td className="actions">
        <button type="button" className="btn-manage" onClick={() => setManage(true)}>
          <Icon.Edit size={14} /> <span>Manage</span>
        </button>
        {manage && (
          <UserManageModal
            user={user}
            onClose={() => setManage(false)}
            onChanged={onChanged}
          />
        )}
      </td>
    </tr>
  );
}

const formatCores = (milli: number) => {
  const c = milli / 1000;
  return Number.isInteger(c) ? String(c) : c.toFixed(1);
};

// UserManageModal is the single place to manage everything about a user: role,
// per-environment access + quota (local host + each remote), password, and
// deletion. Quota is per environment, so it can never be edited as one number.
type EnvGrantForm = { checked: boolean; cpu: number; mem: number };
function UserManageModal({ user, onClose, onChanged }: { user: User; onClose: () => void; onChanged: () => void }) {
  const [envs] = useAsync<Endpoint[]>(() => api.endpoints(), []);
  const [role, setRole] = useState<Role>(user.role);
  const [localCpu, setLocalCpu] = useState(user.cpuQuotaMilli / 1000);
  const [localMem, setLocalMem] = useState(user.memQuotaMB);
  const [grants, setGrants] = useState<Record<number, EnvGrantForm> | null>(null);
  const [newPw, setNewPw] = useState("");
  const [pwBusy, pwRun] = useAction();
  const [busy, setBusy] = useState(false);
  const isMember = role === "member";

  useEffect(() => {
    api.getUserEnvironments(user.id).then((gs) => {
      const map: Record<number, EnvGrantForm> = {};
      (gs ?? []).forEach((g) => (map[g.endpointId] = { checked: true, cpu: g.cpuQuotaMilli / 1000, mem: g.memQuotaMB }));
      setGrants(map);
    }).catch(() => setGrants({}));
  }, [user.id]);

  const remotes = (envs ?? []).filter((e) => !e.local);
  const g = (id: number): EnvGrantForm => grants?.[id] ?? { checked: false, cpu: 1, mem: 512 };
  const patch = (id: number, p: Partial<EnvGrantForm>) => setGrants((cur) => ({ ...(cur ?? {}), [id]: { ...g(id), ...p } }));

  const setPassword = async () => {
    if (newPw.length < 6) {
      toast("error", "Password must be at least 6 characters");
      return;
    }
    const r = await pwRun(api.resetPassword(user.id, newPw), { success: `Password reset for ${user.username}` });
    if (r !== undefined) setNewPw("");
  };

  // Save applies role, then (for members) the local quota and the remote
  // environment grants together, so the modal is one atomic "manage" action.
  const save = async () => {
    setBusy(true);
    try {
      if (role !== user.role) {
        await api.setUserRole(user.id, role);
      }
      if (role === "member") {
        await api.updateUserQuota(user.id, Math.round(localCpu * 1000), localMem);
        const payload = remotes
          .filter((e) => grants?.[e.id]?.checked)
          .map((e) => ({ endpointId: e.id, cpuQuotaMilli: Math.round(g(e.id).cpu * 1000), memQuotaMB: g(e.id).mem }));
        await api.setUserEnvironments(user.id, payload);
      }
      toast("success", `Saved ${user.username}`);
      onChanged();
      onClose();
    } catch (e) {
      toast("error", String(e));
    } finally {
      setBusy(false);
    }
  };

  const del = async () => {
    if (!window.confirm(`Delete ${user.username}? This cannot be undone.`)) return;
    const r = await run(api.deleteUser(user.id), { success: `Deleted ${user.username}` });
    if (r !== undefined) {
      onChanged();
      onClose();
    }
  };

  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong>
            <Icon.Users size={15} /> Manage · {user.username}
          </strong>
          <button type="button" onClick={onClose} aria-label="Close">
            <Icon.Close size={16} />
          </button>
        </div>
        <div className="modal-pad user-manage">
          {/* Role */}
          <section className="um-section">
            <h4>Role</h4>
            <div className="seg">
              {(["member", "admin"] as Role[]).map((r) => (
                <button
                  key={r}
                  type="button"
                  className={role === r ? "seg-on" : ""}
                  onClick={() => setRole(r)}
                >
                  {r}
                </button>
              ))}
            </div>
            <p className="hint">
              {isMember
                ? "Members are quota-bound and see only resources they own."
                : "Admins have unlimited quota and full access to every environment. (The last admin can't be demoted.)"}
            </p>
          </section>

          {/* Quota & environments — members only */}
          {isMember && (
            <section className="um-section">
              <h4>Access &amp; quota per environment</h4>
              <p className="hint">Each environment has its own CPU/RAM quota. The local host is always available; grant remote hosts below.</p>

              <div className="env-grant um-local">
                <div className="check">
                  <Icon.Server size={15} />
                  <span className="strong">Local host</span>
                  <span className="muted">always available</span>
                </div>
                <div className="env-grant-quota">
                  <label>
                    CPU (cores)
                    <input type="number" step="0.1" min="0" value={localCpu} onChange={(e) => setLocalCpu(Number(e.target.value))} />
                  </label>
                  <label>
                    Memory (MB)
                    <input type="number" min="0" value={localMem} onChange={(e) => setLocalMem(Number(e.target.value))} />
                  </label>
                </div>
              </div>

              {!grants || !envs ? (
                <Loading />
              ) : remotes.length === 0 ? (
                <p className="muted">No remote environments yet — add one in the Environments tab.</p>
              ) : (
                <ul className="check-list">
                  {remotes.map((e) => {
                    const gf = g(e.id);
                    return (
                      <li key={e.id} className="env-grant">
                        <label className="check">
                          <input type="checkbox" checked={gf.checked} onChange={(ev) => patch(e.id, { checked: ev.target.checked })} />
                          <span className="strong">{e.name}</span>
                          <span className="muted mono">{e.host}</span>
                        </label>
                        {gf.checked && (
                          <div className="env-grant-quota">
                            <label>
                              CPU (cores)
                              <input type="number" step="0.1" min="0" value={gf.cpu} onChange={(ev) => patch(e.id, { cpu: Number(ev.target.value) })} />
                            </label>
                            <label>
                              Memory (MB)
                              <input type="number" min="0" value={gf.mem} onChange={(ev) => patch(e.id, { mem: Number(ev.target.value) })} />
                            </label>
                          </div>
                        )}
                      </li>
                    );
                  })}
                </ul>
              )}
            </section>
          )}

          {/* Password */}
          <section className="um-section">
            <h4>Reset password</h4>
            <div className="um-pw">
              <input
                type="password"
                value={newPw}
                placeholder="New password (min 6 chars)"
                onChange={(e) => setNewPw(e.target.value)}
              />
              <button type="button" onClick={setPassword} disabled={pwBusy || newPw.length < 6}>
                {pwBusy ? <Icon.Spinner size={15} /> : <Icon.Key size={15} />}
                <span>Set password</span>
              </button>
            </div>
          </section>

          {/* Danger */}
          <section className="um-section um-danger">
            <div>
              <h4>Delete user</h4>
              <p className="hint">Removes the account. Their containers/services are not deleted.</p>
            </div>
            <button type="button" className="danger" onClick={del}>
              <Icon.Trash size={15} /> <span>Delete</span>
            </button>
          </section>

          <div className="actions" style={{ justifyContent: "flex-end", marginTop: 4 }}>
            <button type="button" onClick={onClose} disabled={busy}>
              Cancel
            </button>
            <button type="button" className="primary" onClick={save} disabled={busy || (isMember && !grants)}>
              {busy ? <Icon.Spinner size={15} /> : <Icon.Check2 size={15} />}
              <span>Save changes</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function Requests({ isAdmin, onReviewed }: { isAdmin: boolean; onReviewed: () => void }) {
  const [list, err, reload] = useAsync<ResourceRequest[]>(() => api.requests(), []);
  const [envs] = useAsync<Endpoint[]>(() => api.endpoints(), []);
  const [form, setForm] = useState({ endpointId: 0, cpuCores: 1, memMB: 512, note: "" });
  const [busy, setBusy] = useState(false);
  const envName = (id: number) => (envs ?? []).find((e) => e.id === id)?.name ?? (id === 0 ? "Local" : `env ${id}`);

  const fileRequest = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    const res = await run(
      api.createRequest({ endpointId: form.endpointId, cpuMilli: Math.round(form.cpuCores * 1000), memMB: form.memMB, note: form.note }),
      { success: "Request submitted" }
    );
    setBusy(false);
    if (res) {
      setForm({ endpointId: 0, cpuCores: 1, memMB: 512, note: "" });
      reload();
    }
  };

  return (
    <div className="expose">
      {!isAdmin && (
        <section>
          <h3>Request resources</h3>
          <form className="form" onSubmit={fileRequest}>
            <div className="row">
              <label>
                Environment
                <select value={form.endpointId} onChange={(e) => setForm({ ...form, endpointId: Number(e.target.value) })}>
                  {(envs ?? [{ id: 0, name: "Local" } as Endpoint]).map((e) => (
                    <option key={e.id} value={e.id}>
                      {e.name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                CPU (cores)
                <input type="number" step="0.1" min="0" value={form.cpuCores} onChange={(e) => setForm({ ...form, cpuCores: Number(e.target.value) })} />
              </label>
              <label>
                Memory (MB)
                <input type="number" min="0" value={form.memMB} onChange={(e) => setForm({ ...form, memMB: Number(e.target.value) })} />
              </label>
            </div>
            <label>
              Reason (optional)
              <input placeholder="What is this for?" value={form.note} onChange={(e) => setForm({ ...form, note: e.target.value })} />
            </label>
            <button type="submit" className="primary" disabled={busy}>
              {busy ? <Icon.Spinner size={15} /> : <Icon.Inbox size={15} />}
              <span>Submit request</span>
            </button>
            <p className="hint">An admin reviews your request. Once approved, the granted CPU/RAM becomes your quota on that environment.</p>
          </form>
        </section>
      )}

      <section>
        <h3>{isAdmin ? "Resource requests" : "My requests"}</h3>
        {err && <Err msg={err} onRetry={reload} />}
        {!list ? (
          <TableSkeleton cols={6} />
        ) : list.length === 0 ? (
          <Empty icon={Icon.Inbox} title="No requests" />
        ) : (
          <div className="table-wrap"><table>
            <thead>
              <tr>
                {isAdmin && <th>User</th>}
                <th>Environment</th>
                <th>Requested</th>
                <th>Reason</th>
                <th>Status</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {list.map((rq) => (
                <RequestRow
                  key={rq.id}
                  rq={rq}
                  isAdmin={isAdmin}
                  envName={envName(rq.endpointId)}
                  onReviewed={() => {
                    reload();
                    onReviewed();
                  }}
                />
              ))}
            </tbody>
          </table></div>
        )}
      </section>
    </div>
  );
}

function RequestRow({ rq, isAdmin, envName, onReviewed }: { rq: ResourceRequest; isAdmin: boolean; envName: string; onReviewed: () => void }) {
  const pending = rq.status === "pending";
  return (
    <tr>
      {isAdmin && <td className="strong">{rq.username}</td>}
      <td>
        <span className="badge">
          <Icon.Server size={12} /> {envName}
        </span>
      </td>
      <td className="muted">
        {(rq.cpuMilli / 1000).toFixed(1)} CPU · {rq.memMB} MB
      </td>
      <td className="muted">{rq.note || "—"}</td>
      <td>
        <span className={`badge status-${rq.status}`}>{rq.status}</span>
      </td>
      <td className="actions">
        {isAdmin && pending ? (
          <>
            <ActionButton
              icon={Icon.Check2}
              label="Approve"
              variant="primary"
              success={`Approved ${rq.username}`}
              task={() => api.reviewRequest(rq.id, { approve: true })}
              onDone={onReviewed}
            />
            <ActionButton
              icon={Icon.Close}
              label="Reject"
              variant="danger"
              success={`Rejected ${rq.username}`}
              task={() => api.reviewRequest(rq.id, { approve: false })}
              onDone={onReviewed}
            />
          </>
        ) : (
          rq.reviewedBy && <span className="muted">by {rq.reviewedBy}</span>
        )}
      </td>
    </tr>
  );
}

const matchService = (s: Service, q: string) =>
  s.name.toLowerCase().includes(q) ||
  s.image.toLowerCase().includes(q) ||
  (s.subdomain ?? "").toLowerCase().includes(q) ||
  (s.domains ?? []).some((d) => d.toLowerCase().includes(q));

function Services({ me, onChanged }: { me: Me | null; onChanged: () => void }) {
  const [list, err, reload] = useAsync<Service[]>(() => api.services(), []);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<Service | null>(null);
  const [detailName, setDetailName] = useState<string | null>(null);
  const isMember = me?.role === "member";
  const sp = useSearchPage(list ?? [], matchService, 9);
  const detailSvc = detailName ? list?.find((s) => s.name === detailName) ?? null : null;

  const refreshAll = () => {
    reload();
    onChanged();
  };

  if (err) return <Err msg={err} onRetry={reload} />;
  if (!list) return (<><EdgeBanner /><CardSkeleton count={4} /></>);

  return (
    <>
      <EdgeBanner />
      <div className="toolbar">
        <button type="button" className="primary" onClick={() => setCreating(true)}>
          <Icon.Plus size={15} />
          <span>New service</span>
        </button>
        <button type="button" onClick={reload}>
          <Icon.Refresh size={15} />
          <span>Refresh</span>
        </button>
        {list.length > 0 && <SearchInput value={sp.query} onChange={sp.setQuery} placeholder="Search services…" />}
        <span className="count">{sp.filtered.length} of {list.length} service{list.length === 1 ? "" : "s"}</span>
      </div>

      {list.length === 0 ? (
        <Empty
          icon={Icon.Layers}
          title="No services yet"
          hint="A service runs N load-balanced replicas behind one subdomain, with optional autoscaling and git deploys."
        />
      ) : sp.filtered.length === 0 ? (
        <Empty icon={Icon.Search} title="No matches" hint="No services match your search." />
      ) : (
        <>
          <div className="svc-grid">
            {sp.pageItems.map((svc) => (
              <ServiceCard
                key={svc.id}
                svc={svc}
                onEdit={() => setEditing(svc)}
                onOpen={() => setDetailName(svc.name)}
                onChanged={refreshAll}
              />
            ))}
          </div>
          <Pager page={sp.page} pageCount={sp.pageCount} onPage={sp.setPage} />
        </>
      )}

      {(creating || editing) && (
        <ServiceEditor
          isMember={!!isMember}
          editing={editing}
          onClose={() => {
            setCreating(false);
            setEditing(null);
          }}
          onCreated={() => {
            setCreating(false);
            setEditing(null);
            refreshAll();
          }}
        />
      )}

      {detailSvc && (
        <ServiceDetail
          svc={detailSvc}
          onClose={() => setDetailName(null)}
          onChanged={refreshAll}
          onEdit={() => {
            setEditing(detailSvc);
            setDetailName(null);
          }}
        />
      )}
    </>
  );
}

function ServiceCard({ svc, onEdit, onOpen, onChanged }: { svc: Service; onEdit: () => void; onOpen: () => void; onChanged: () => void }) {
  const [scaleBusy, scaleRun] = useAction();
  const [subBusy, subRun] = useAction();
  const webhookURL = `${location.origin}/api/hooks/${svc.webhookToken}`;

  const scaleTo = (n: number) => scaleRun(api.scaleService(svc.name, n), { success: `Scaled ${svc.name} to ${n}` }).then(onChanged);

  const editSubdomain = async () => {
    const next = window.prompt(
      "Custom subdomain — adds <subdomain>.<domain> alongside the automatic host. Leave blank to remove it.",
      svc.subdomain || "",
    );
    if (next === null) return;
    const v = next.trim();
    const ok = await subRun(api.setServiceSubdomain(svc.name, v), {
      success: v ? `Custom subdomain set to ${v}` : "Custom subdomain removed",
    });
    if (ok !== undefined) onChanged();
  };
  const copyHost = (h: string) => navigator.clipboard?.writeText(h).then(() => toast("success", `Copied ${h}`));

  return (
    <div className="svc-card">
      <div className="svc-head">
        <button type="button" className="svc-open" onClick={onOpen} title="View structure & replicas">
          <span className="svc-name">
            <Icon.Layers size={15} /> {svc.name}
          </span>
          <span className="muted mono svc-image">{svc.image}</span>
        </button>
        {svc.autoscale ? (
          <span className="badge running" title={`Autoscale ${svc.minReplicas}–${svc.maxReplicas} @ ${svc.targetCpuPercent}% CPU`}>
            <Icon.Cpu size={12} /> auto {svc.minReplicas}–{svc.maxReplicas}
          </span>
        ) : (
          <span className="badge">manual</span>
        )}
      </div>

      <div className="svc-meta">
        <div className="svc-domains">
          {(svc.domains ?? []).map((d, i) => (
            <button
              key={d}
              type="button"
              className="svc-domain"
              title={`${i === 0 ? "Automatic host" : "Custom subdomain"} — click to copy`}
              onClick={() => copyHost(d)}
            >
              <Icon.Globe size={13} />
              <span className="mono svc-domain-name">{d}</span>
              {i === 0 && <span className="dom-tag">auto</span>}
            </button>
          ))}
          <button
            type="button"
            className="btn-icon dom-edit"
            title={svc.subdomain ? "Edit custom subdomain" : "Add a custom subdomain"}
            disabled={subBusy}
            onClick={editSubdomain}
          >
            {subBusy ? <Icon.Spinner size={13} /> : <Icon.Edit size={13} />}
          </button>
        </div>
        <span className="muted">
          {(svc.cpuMilli / 1000).toFixed(2)} CPU / {svc.memMB} MB per replica · port {svc.containerPort}
        </span>
        {svc.gitRepo && (
          <span className="muted mono svc-git" title={svc.gitRepo}>
            <Icon.Git size={13} /> {svc.gitBranch}
          </span>
        )}
      </div>

      <div className="svc-replicas">
        <span className="muted">Replicas</span>
        <div className="stepper">
          <button type="button" title="Scale down" disabled={scaleBusy || svc.replicas <= 0} onClick={() => scaleTo(svc.replicas - 1)}>
            <Icon.Minus size={14} />
          </button>
          <span className="stepper-val">{scaleBusy ? <Icon.Spinner size={14} /> : svc.replicas}</span>
          <button type="button" title="Scale up" disabled={scaleBusy} onClick={() => scaleTo(svc.replicas + 1)}>
            <Icon.Plus size={14} />
          </button>
        </div>
        <span className="lb-dots" title={`${svc.replicas} replicas load-balanced`}>
          {Array.from({ length: Math.min(svc.replicas, 8) }).map((_, i) => (
            <span key={i} className="lb-dot" />
          ))}
        </span>
      </div>

      {svc.gitRepo && (
        <div className="svc-webhook">
          <span className="muted">
            <Icon.Webhook size={13} /> Push webhook
          </span>
          <input readOnly value={webhookURL} onFocus={(e) => e.currentTarget.select()} />
          <button
            type="button"
            title="Copy"
            onClick={() => navigator.clipboard?.writeText(webhookURL).then(() => toast("success", "Webhook URL copied"))}
          >
            Copy
          </button>
        </div>
      )}

      <div className="actions svc-actions">
        <button type="button" onClick={onEdit} title="Edit configuration">
          <Icon.Edit size={14} /> <span>Edit</span>
        </button>
        <ActionButton
          icon={Icon.Update}
          label={svc.gitRepo ? "Build & redeploy" : "Redeploy"}
          title={svc.gitRepo ? "Clone repo, build image, roll out" : "Re-pull image and roll out"}
          success={svc.gitRepo ? `Building ${svc.name}…` : `Redeployed ${svc.name}`}
          task={() => api.redeployService(svc.name)}
          onDone={onChanged}
        />
        <ActionButton
          icon={Icon.Trash}
          title="Delete service"
          variant="danger"
          confirm={`Delete service ${svc.name} and all its replicas?`}
          success={`Deleted ${svc.name}`}
          task={() => api.deleteService(svc.name)}
          onDone={onChanged}
        />
      </div>
    </div>
  );
}

// ServiceDetail visualizes a service's routing structure — domains → load
// balancer → replica containers — plus its configuration, opened by clicking a
// service card. Replicas show live state and open the container detail on click.
function ServiceDetail({
  svc,
  onClose,
  onChanged,
  onEdit,
}: {
  svc: Service;
  onClose: () => void;
  onChanged: () => void;
  onEdit: () => void;
}) {
  const [containers] = useAsync<Container[]>(() => api.containers(), []);
  const [replicaDetail, setReplicaDetail] = useState<Container | null>(null);
  const byName = new Map((containers ?? []).map((c) => [c.Names?.[0]?.replace(/^\//, "") ?? "", c]));
  const replicas = Array.from({ length: svc.replicas }, (_, i) => {
    const rn = `${svc.name}-${i}`;
    return { name: rn, c: byName.get(rn) ?? null };
  });
  const runningCount = replicas.filter((r) => r.c?.State === "running").length;

  return (
    <>
    <div className="modal" onClick={onClose}>
      <div className="modal-body wide cdetail" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong>
            <Icon.Layers size={15} /> {svc.name}
            <span className={`badge ${runningCount === svc.replicas && svc.replicas > 0 ? "running" : ""}`}>
              <span className="badge-dot" /> {runningCount}/{svc.replicas} up
            </span>
          </strong>
          <button type="button" onClick={onClose} aria-label="Close">
            <Icon.Close size={16} />
          </button>
        </div>

        <div className="cdetail-actions">
          <button type="button" onClick={onEdit}>
            <Icon.Edit size={14} /> <span>Edit</span>
          </button>
          <ActionButton
            icon={Icon.Update}
            label={svc.gitRepo ? "Build & redeploy" : "Redeploy"}
            success={svc.gitRepo ? `Building ${svc.name}…` : `Redeployed ${svc.name}`}
            task={() => api.redeployService(svc.name)}
            onDone={onChanged}
          />
          <ActionButton
            icon={Icon.Trash}
            label="Delete"
            variant="danger"
            confirm={`Delete service ${svc.name} and all its replicas?`}
            success={`Deleted ${svc.name}`}
            task={() => api.deleteService(svc.name)}
            onDone={() => {
              onChanged();
              onClose();
            }}
          />
        </div>

        <div className="cdetail-body">
          <div className="cdetail-overview">
            {/* Topology: domains → load balancer → replicas */}
            <div className="svc-flow">
              <div className="flow-node">
                <div className="flow-node-title">
                  <Icon.Globe size={14} /> Request routing
                </div>
                <div className="flow-domains">
                  {(svc.domains ?? []).map((d, i) => (
                    <span key={d} className="svc-domain static">
                      <span className="mono svc-domain-name">{d}</span>
                      {i === 0 && <span className="dom-tag">auto</span>}
                    </span>
                  ))}
                  {(svc.domains ?? []).length === 0 && <span className="muted">No domains</span>}
                </div>
              </div>

              <div className="flow-conn" />

              <div className="flow-node accent">
                <div className="flow-node-title">
                  <Icon.Layers size={14} /> Load balancer
                </div>
                <div className="muted">
                  Envoy · round-robin · container port {svc.containerPort} ·{" "}
                  {svc.autoscale ? `autoscale ${svc.minReplicas}–${svc.maxReplicas} @ ${svc.targetCpuPercent}%` : "manual scale"}
                </div>
              </div>

              <div className="flow-conn branch" />

              <div className="flow-replicas">
                {replicas.map((r) => {
                  const state = r.c?.State ?? "pending";
                  return (
                    <button
                      key={r.name}
                      type="button"
                      className="flow-replica"
                      disabled={!r.c}
                      title={r.c ? "Open container detail" : "Not created yet"}
                      onClick={() => r.c && setReplicaDetail(r.c)}
                    >
                      <Icon.Box size={14} />
                      <span className="mono flow-replica-name">{r.name}</span>
                      <span className={`badge ${state}`}>
                        <span className="badge-dot" /> {state}
                      </span>
                    </button>
                  );
                })}
              </div>
            </div>

            {/* Configuration */}
            <dl className="kv">
              <div>
                <dt>Image</dt>
                <dd className="mono">{svc.image}</dd>
              </div>
              <div>
                <dt>Replicas</dt>
                <dd>{svc.replicas}</dd>
              </div>
              <div>
                <dt>Resources / replica</dt>
                <dd>
                  {(svc.cpuMilli / 1000).toFixed(2)} CPU · {svc.memMB} MB
                </dd>
              </div>
              <div>
                <dt>Network</dt>
                <dd className="mono">{svc.network || "—"}</dd>
              </div>
              <div>
                <dt>Custom subdomain</dt>
                <dd>{svc.subdomain || "—"}</dd>
              </div>
              {svc.gitRepo && (
                <div>
                  <dt>Git</dt>
                  <dd className="mono">
                    {svc.gitRepo} @ {svc.gitBranch}
                  </dd>
                </div>
              )}
            </dl>
          </div>
        </div>
      </div>
    </div>
    {replicaDetail && (
      <ContainerDetail
        container={replicaDetail}
        onClose={() => setReplicaDetail(null)}
        onChanged={onChanged}
        onEdit={() => setReplicaDetail(null)}
      />
    )}
    </>
  );
}

type SectionKey = "basic" | "source" | "scaling" | "advanced";

function ServiceEditor({
  isMember,
  editing,
  onClose,
  onCreated,
}: {
  isMember: boolean;
  editing?: Service | null;
  onClose: () => void;
  onCreated: () => void;
}) {
  // `advanced` is managed separately (adv state) and merged at submit.
  const [f, setF] = useState<Omit<ServiceRequest, "advanced">>(() =>
    editing
      ? {
          name: editing.name,
          image: editing.image,
          subdomain: editing.subdomain,
          containerPort: editing.containerPort,
          network: editing.network || "easydeploy-edge",
          env: [],
          cpuMilli: editing.cpuMilli,
          memMB: editing.memMB,
          replicas: editing.replicas,
          minReplicas: editing.minReplicas,
          maxReplicas: editing.maxReplicas,
          autoscale: editing.autoscale,
          targetCpuPercent: editing.targetCpuPercent,
          gitRepo: editing.gitRepo,
          gitBranch: editing.gitBranch || "main",
          gitDockerfile: editing.gitDockerfile || "Dockerfile",
        }
      : {
          name: "",
          image: "nginx:alpine",
          subdomain: "",
          containerPort: 80,
          network: "easydeploy-edge",
          env: [],
          cpuMilli: isMember ? 200 : 0,
          memMB: isMember ? 128 : 0,
          replicas: 1,
          minReplicas: 1,
          maxReplicas: 4,
          autoscale: false,
          targetCpuPercent: 60,
          gitRepo: "",
          gitBranch: "main",
          gitDockerfile: "Dockerfile",
        },
  );
  const [source, setSource] = useState<"image" | "git">(editing?.gitRepo ? "git" : "image");
  const [envText, setEnvText] = useState(() => {
    if (!editing?.env) return "";
    try {
      return (JSON.parse(editing.env) as string[]).join("\n");
    } catch {
      return "";
    }
  });
  const [busy, setBusy] = useState(false);
  const [adv, setAdv] = useState<AdvForm>(() => (editing ? advToForm(editing.advanced) : emptyAdvForm()));
  const [networkNames, setNetworkNames] = useState<string[]>([]);
  useEffect(() => {
    if (isMember) return; // networks are admin-only
    let live = true;
    api.networks().then((ns) => live && setNetworkNames((ns ?? []).map((n) => n.Name))).catch(() => {});
    return () => {
      live = false;
    };
  }, [isMember]);
  const [open, setOpen] = useState<Record<SectionKey, boolean>>({
    basic: true,
    source: true,
    scaling: false,
    advanced: false,
  });
  const toggle = (k: SectionKey) => setOpen((o) => ({ ...o, [k]: !o[k] }));
  const update = (patch: Partial<typeof f>) => setF((prev) => ({ ...prev, ...patch }));

  // Completion state drives the green checks in the section headers.
  const basicDone = f.name.trim() !== "";
  const sourceDone = source === "image" ? f.image.trim() !== "" : f.gitRepo.trim() !== "";
  const scalingDone = !isMember || (f.cpuMilli > 0 && f.memMB > 0);
  const canSubmit = basicDone && sourceDone && scalingDone;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    setBusy(true);
    const env = envText.split("\n").map((l) => l.trim()).filter(Boolean);
    // In image mode, clear any git repo so the backend treats it as image-based.
    const payload = { ...f, gitRepo: source === "git" ? f.gitRepo : "", env, advanced: buildAdvanced(adv) };
    const res = editing
      ? await run(api.updateService(editing.name, payload), { success: `Updated service ${editing.name}` })
      : await run(api.createService(payload), { success: `Created service ${f.name}` });
    setBusy(false);
    if (res) onCreated();
  };

  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body wizard" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong>
            <Icon.Layers size={15} /> {editing ? `Edit service · ${editing.name}` : "New service"}
          </strong>
          <button type="button" onClick={onClose} aria-label="Close">
            <Icon.Close size={16} />
          </button>
        </div>
        {busy && <div className="progress" />}
        <form className="wizard-body modal-pad" onSubmit={submit}>
          {/* 1. Basic information */}
          <Section
            icon={Icon.Box}
            title="Basic information"
            subtitle="Name and expose your service"
            done={basicDone}
            open={open.basic}
            onToggle={() => toggle("basic")}
          >
            <Field label="Service name" required help={editing ? "The name identifies the service and can't be changed after creation." : "A unique name; replica containers are named <name>-0, <name>-1, …"}>
              <input required value={f.name} placeholder="my-app" disabled={!!editing} onChange={(e) => update({ name: e.target.value })} />
            </Field>
            <div className="row">
              <Field label="Custom subdomain (optional)" help="Optional vanity host <subdomain>.<base-domain>. The service always gets an automatic host <name>.<server>.<base-domain> too. You can set or change this later.">
                <input value={f.subdomain} placeholder="(optional)" onChange={(e) => update({ subdomain: e.target.value })} />
              </Field>
              <Field label="Container port" help="The port your app listens on inside the container">
                <input type="number" value={f.containerPort} onChange={(e) => update({ containerPort: Number(e.target.value) })} />
              </Field>
              <Field label="Network">
                <SearchPicker
                  value={f.network}
                  options={networkNames}
                  onChange={(v) => update({ network: v })}
                  placeholder="pick or type a network"
                  icon={Icon.Network}
                />
              </Field>
            </div>
          </Section>

          {/* 2. Source */}
          <Section
            icon={Icon.Git}
            title="Source"
            subtitle="Where the container image comes from"
            done={sourceDone}
            open={open.source}
            onToggle={() => toggle("source")}
          >
            <SourceSelector
              value={source}
              onChange={(k) => setSource(k as "image" | "git")}
              options={[
                { key: "image", icon: Icon.Registry, tag: "Deployment", title: "Deploy a Docker image" },
                { key: "git", icon: Icon.Git, tag: "Combined", title: "Build & deploy a Git repo" },
              ]}
            />
            {source === "image" ? (
              <Field label="Image" required help="No registry to pick — auth is automatic. Include the host for private images (e.g. ghcr.io/you/app:tag) and EasyDeploy matches it to a configured registry's credentials. Public images like nginx:alpine need nothing.">
                <input required value={f.image} placeholder="nginx:alpine  ·  ghcr.io/you/app:tag" onChange={(e) => update({ image: e.target.value })} />
              </Field>
            ) : (
              <>
                <Field label="Repository URL" required help="Cloned on each webhook / redeploy, then built with the Docker SDK">
                  <input required value={f.gitRepo} placeholder="https://github.com/you/app.git" onChange={(e) => update({ gitRepo: e.target.value })} />
                </Field>
                <div className="row">
                  <Field label="Branch">
                    <input value={f.gitBranch} onChange={(e) => update({ gitBranch: e.target.value })} />
                  </Field>
                  <Field label="Dockerfile">
                    <input value={f.gitDockerfile} onChange={(e) => update({ gitDockerfile: e.target.value })} />
                  </Field>
                </div>
                <p className="hint">A push webhook URL is generated after creation, so `git push` rebuilds and rolls out automatically.</p>
              </>
            )}
            <Field label="Environment" hint={<span className="muted">KEY=VALUE per line</span>}>
              <textarea rows={3} value={envText} onChange={(e) => setEnvText(e.target.value)} />
            </Field>
          </Section>

          {/* 3. Replicas, scaling & resources */}
          <Section
            icon={Icon.Layers}
            title="Replicas & resources"
            subtitle={f.autoscale ? `Autoscale ${f.minReplicas}–${f.maxReplicas} on CPU` : `${f.replicas} replica${f.replicas === 1 ? "" : "s"}, load-balanced`}
            done={scalingDone}
            open={open.scaling}
            onToggle={() => toggle("scaling")}
          >
            <div className="row">
              <Field label="Replicas" help="Identical containers Envoy round-robins across">
                <input type="number" min="0" value={f.replicas} onChange={(e) => update({ replicas: Number(e.target.value) })} />
              </Field>
              <label className="check auto-toggle">
                <input type="checkbox" checked={f.autoscale} onChange={(e) => update({ autoscale: e.target.checked })} />
                <span>Autoscale on CPU</span>
              </label>
            </div>
            {f.autoscale && (
              <div className="row">
                <Field label="Min replicas">
                  <input type="number" min="1" value={f.minReplicas} onChange={(e) => update({ minReplicas: Number(e.target.value) })} />
                </Field>
                <Field label="Max replicas">
                  <input type="number" min="1" value={f.maxReplicas} onChange={(e) => update({ maxReplicas: Number(e.target.value) })} />
                </Field>
                <Field label="Target CPU %">
                  <input type="number" min="1" max="100" value={f.targetCpuPercent} onChange={(e) => update({ targetCpuPercent: Number(e.target.value) })} />
                </Field>
              </div>
            )}
            <div className="row">
              <Field label="CPU per replica (cores)" required={isMember}>
                <input type="number" step="0.1" min="0" required={isMember} value={f.cpuMilli / 1000} onChange={(e) => update({ cpuMilli: Math.round(Number(e.target.value) * 1000) })} />
              </Field>
              <Field label="Memory per replica (MB)" required={isMember}>
                <input type="number" min="0" required={isMember} value={f.memMB} onChange={(e) => update({ memMB: Number(e.target.value) })} />
              </Field>
            </div>
          </Section>

          {/* 4. Advanced Docker options */}
          <Section
            icon={Icon.Cpu}
            title="Advanced Docker options"
            subtitle="Mounts, ports, capabilities, healthcheck, and more"
            open={open.advanced}
            onToggle={() => toggle("advanced")}
          >
            <AdvancedPanel form={adv} onChange={setAdv} canPickVolumes={!isMember} />
          </Section>
        </form>

        <div className="wizard-foot">
          <span className="muted wizard-status">
            {canSubmit ? (
              <>
                <Icon.Check size={14} /> {editing ? "Ready to save" : "Ready to create"}
              </>
            ) : (
              "Fill the required fields to continue"
            )}
          </span>
          <div className="actions">
            <button type="button" onClick={onClose} disabled={busy}>
              Cancel
            </button>
            <button type="button" className="primary" disabled={busy || !canSubmit} onClick={submit}>
              {busy ? <Icon.Spinner size={15} /> : <Icon.Layers size={15} />}
              <span>{busy ? (editing ? "Saving…" : "Creating…") : editing ? "Save changes" : "Create service"}</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function fmtBytes(n: number): string {
  if (n < 0) return "—";
  if (n < 1024) return `${n} B`;
  const u = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${u[i]}`;
}

const matchVolume = (v: DockerVolume, q: string) => v.name.toLowerCase().includes(q) || v.driver.toLowerCase().includes(q);

function Volumes() {
  const [list, err, reload] = useAsync<DockerVolume[]>(() => api.volumes(), []);
  const [usage, setUsage] = useState<Record<string, { size: number; refCount: number }> | null>(null);
  const [creating, setCreating] = useState(false);
  const [browse, setBrowse] = useState<DockerVolume | null>(null);
  const sp = useSearchPage(list ?? [], matchVolume, 12);

  // Size / ref-count are computed by the (slow) DiskUsage call, fetched lazily
  // after the list renders so the tab is instant even on busy remote hosts.
  useEffect(() => {
    if (!list) return;
    let live = true;
    setUsage(null);
    api.volumeUsage().then((u) => live && setUsage(u)).catch(() => live && setUsage({}));
    return () => {
      live = false;
    };
  }, [list]);

  if (err) return <Err msg={err} onRetry={reload} />;
  if (!list) return <TableSkeleton cols={5} />;

  return (
    <>
      <div className="toolbar">
        <button type="button" className="primary" onClick={() => setCreating(true)}>
          <Icon.Plus size={15} /> <span>New volume</span>
        </button>
        {list.length > 0 && <SearchInput value={sp.query} onChange={sp.setQuery} placeholder="Search volumes…" />}
        <span className="count">
          {list.length > 0 ? `${sp.filtered.length} of ${list.length}` : "0"} volume{list.length === 1 ? "" : "s"}
        </span>
      </div>
      {creating && (
        <NameCreateModal
          title="New volume"
          label="Volume name"
          placeholder="my-volume"
          hint="Created with the local driver on this host."
          onCreate={(n) => run(api.createVolume(n), { success: `Created volume ${n}` }).then((r) => (r !== undefined ? (reload(), r) : undefined))}
          onClose={() => setCreating(false)}
        />
      )}

      {list.length === 0 ? (
        <Empty icon={Icon.Drive} title="No volumes" hint="Create one above, or deploy a service with a volume mount." />
      ) : sp.filtered.length === 0 ? (
        <Empty icon={Icon.Search} title="No matches" hint="No volumes match your search." />
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Driver</th>
                <th>Size</th>
                <th>In use</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {sp.pageItems.map((v) => {
                const u = usage?.[v.name];
                const refs = u ? u.refCount : -1;
                return (
                  <tr key={v.name}>
                    <td className="strong mono" title={v.mountpoint}>{v.name}</td>
                    <td className="muted">{v.driver}</td>
                    <td className="mono">{u ? fmtBytes(u.size) : usage ? "—" : <span className="muted">…</span>}</td>
                    <td>
                      {refs > 0 ? (
                        <span className="badge running">{refs} container{refs === 1 ? "" : "s"}</span>
                      ) : refs === 0 ? (
                        <span className="badge">unused</span>
                      ) : (
                        <span className="muted">…</span>
                      )}
                    </td>
                    <td className="actions">
                      <button type="button" className="btn-icon" title="Browse files" onClick={() => setBrowse(v)}>
                        <Icon.Folder size={15} />
                      </button>
                      <ActionButton
                        icon={Icon.Trash}
                        title="Delete volume"
                        variant="danger"
                        confirm={refs > 0 ? `${v.name} is in use by ${refs} container(s). Force delete?` : `Delete volume ${v.name}?`}
                        success={`Deleted ${v.name}`}
                        task={() => api.removeVolume(v.name, refs > 0)}
                        onDone={reload}
                      />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {list.length > 0 && sp.filtered.length > 0 && <Pager page={sp.page} pageCount={sp.pageCount} onPage={sp.setPage} />}
      {browse && <VolumeBrowser volume={browse} onClose={() => setBrowse(null)} />}
    </>
  );
}

function VolumeBrowser({ volume, onClose }: { volume: DockerVolume; onClose: () => void }) {
  const [path, setPath] = useState("/");
  const [files, setFiles] = useState<VolFile[] | null>(null);
  const [err, setErr] = useState("");
  const [nonce, setNonce] = useState(0);
  const [busy, setBusy] = useState(false);
  const [upload, setUpload] = useState<{ name: string; pct: number; sending: boolean } | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const reload = () => setNonce((n) => n + 1);
  const join = (dir: string, n: string) => (dir.endsWith("/") ? dir : dir + "/") + n;

  useEffect(() => {
    let live = true;
    setFiles(null);
    setErr("");
    api
      .browseVolume(volume.name, path)
      .then((r) => live && setFiles(r.files ?? []))
      .catch((e) => live && setErr(String((e as Error).message || e)));
    return () => {
      live = false;
    };
  }, [volume.name, path, nonce]);

  const enter = (f: VolFile) => {
    if (f.dir) setPath((p) => join(p, f.name));
  };
  const up = () => setPath((p) => p.replace(/\/[^/]+\/?$/, "") || "/");

  const newFolder = async () => {
    const n = window.prompt("New folder name");
    if (!n) return;
    setBusy(true);
    const ok = await run(api.mkdirVolume(volume.name, join(path, n)), { success: `Created ${n}` });
    setBusy(false);
    if (ok !== undefined) reload();
  };
  const onUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    e.target.value = "";
    if (!f) return;
    setBusy(true);
    setUpload({ name: f.name, pct: 0, sending: true });
    const ok = await run(
      api.uploadVolumeFile(volume.name, path, f, (frac) =>
        setUpload({ name: f.name, pct: frac < 0 ? 100 : Math.round(frac * 100), sending: frac >= 0 }),
      ),
      { success: `Uploaded ${f.name}` },
    );
    setBusy(false);
    setUpload(null);
    if (ok !== undefined) reload();
  };
  const del = async (f: VolFile) => {
    if (!window.confirm(`Delete ${f.name}${f.dir ? " and its contents" : ""}?`)) return;
    setBusy(true);
    const ok = await run(api.deleteVolumeFile(volume.name, join(path, f.name)), { success: `Deleted ${f.name}` });
    setBusy(false);
    if (ok !== undefined) reload();
  };
  const download = (f: VolFile) => {
    const a = document.createElement("a");
    a.href = api.downloadVolumeURL(volume.name, join(path, f.name));
    a.download = f.name;
    document.body.appendChild(a);
    a.click();
    a.remove();
  };

  const sorted = files ? [...files].sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1)) : null;

  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body wide" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong>
            <Icon.Drive size={15} /> {volume.name}
          </strong>
          <button type="button" onClick={onClose} aria-label="Close">
            <Icon.Close size={16} />
          </button>
        </div>
        <div className="vol-path">
          <button type="button" className="btn-icon" onClick={up} disabled={path === "/" || busy} title="Up">
            <Icon.Back size={15} />
          </button>
          <span className="mono vol-crumb">{path}</span>
          <div className="vol-tools">
            <button type="button" onClick={newFolder} disabled={busy}>
              <Icon.FolderPlus size={15} /> <span>New folder</span>
            </button>
            <button type="button" onClick={() => fileInput.current?.click()} disabled={busy}>
              {busy ? <Icon.Spinner size={15} /> : <Icon.Upload size={15} />} <span>Upload</span>
            </button>
            <input ref={fileInput} type="file" hidden onChange={onUpload} />
          </div>
        </div>
        {upload && (
          <div className="vol-upload">
            <div className="vol-upload-row">
              <Icon.Upload size={13} />
              <span className="file-name mono">{upload.name}</span>
              <span className="muted">{upload.sending ? `${upload.pct}%` : "writing…"}</span>
            </div>
            <div className="vol-progress">
              <div
                className={`vol-progress-fill${upload.sending ? "" : " indeterminate"}`}
                style={{ width: `${upload.pct}%` }}
              />
            </div>
          </div>
        )}
        <div className="mon-body vol-list">
          {err && <Err msg={err} onRetry={reload} />}
          {!sorted ? (
            <Loading />
          ) : sorted.length === 0 ? (
            <p className="muted">Empty directory.</p>
          ) : (
            <ul className="filelist">
              {sorted.map((f) => (
                <li key={f.name} className="file-item">
                  <button type="button" className={`file-main ${f.dir ? "is-dir" : ""}`} onClick={() => enter(f)} disabled={!f.dir}>
                    {f.dir ? <Icon.Folder size={15} /> : <Icon.File size={15} />}
                    <span className="file-name">{f.name}</span>
                  </button>
                  <span className="muted file-size">{f.dir ? "" : fmtBytes(f.size)}</span>
                  <span className="file-acts">
                    {!f.dir && (
                      <button type="button" className="btn-icon" title="Download" onClick={() => download(f)}>
                        <Icon.Download size={15} />
                      </button>
                    )}
                    <button type="button" className="btn-icon danger" title="Delete" onClick={() => del(f)} disabled={busy}>
                      <Icon.Trash size={15} />
                    </button>
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>
        <p className="hint vol-foot">Files are read/written through a helper container mounted on the volume.</p>
      </div>
    </div>
  );
}

function DocTip({ children }: { children: React.ReactNode }) {
  return (
    <p className="doc-tip">
      <Icon.Alert size={14} /> <span>{children}</span>
    </p>
  );
}

type DocSection = { id: string; title: string; icon: (p: { size?: number }) => JSX.Element; adminOnly?: boolean; body: JSX.Element };

// Docs is the in-app user guide. Sections are role-aware — members see only the
// parts of the app they can use.
function Docs({ isAdmin, onGo }: { isAdmin: boolean; onGo: (t: Tab) => void }) {
  const refs = useRef<Record<string, HTMLElement | null>>({});
  const scrollTo = (id: string) => refs.current[id]?.scrollIntoView({ behavior: "smooth", block: "start" });
  const Go = ({ tab, label }: { tab: Tab; label: string }) => (
    <button type="button" className="doc-link" onClick={() => onGo(tab)}>
      {label} →
    </button>
  );

  const sections: DocSection[] = [
    {
      id: "start",
      title: "Getting started",
      icon: Icon.Gauge,
      body: (
        <>
          <p>EasyDeploy manages Docker across one or more hosts, and can publish a container on its own subdomain through a built-in Envoy proxy.</p>
          <ul>
            <li>The <strong>left sidebar</strong> switches between sections. The <strong>Overview</strong> is your dashboard.</li>
            <li>The <strong>environment switcher</strong> (top-right) picks which Docker host you're working on — everything (containers, services, …) targets the selected host.</li>
            <li>The <strong>health chip</strong> (bottom-left) shows whether the selected host is reachable.</li>
            <li>Your role (<strong>admin</strong> or <strong>member</strong>) is shown top-right and decides what you can see.</li>
          </ul>
        </>
      ),
    },
    {
      id: "containers",
      title: "Containers",
      icon: Icon.Box,
      body: (
        <>
          <p>List and control containers on the selected host. Members see only their own.</p>
          <ul>
            <li><strong>Click a row</strong> to open the detail view: <em>Overview</em> (image, limits, networks, ports, env), <em>Logs</em>, <em>Monitor</em> (live CPU/memory/network), and an interactive <em>Shell</em>.</li>
            <li>Row buttons: start/stop, restart, <em>Edit</em> (reconfigure), <em>Update</em> (pull the latest image and recreate in place), and delete.</li>
            <li>Use <strong>Search</strong> to filter; long lists paginate.</li>
          </ul>
          <Go tab="containers" label="Open Containers" />
        </>
      ),
    },
    {
      id: "services",
      title: "Services (subdomains, scaling, git)",
      icon: Icon.Layers,
      body: (
        <>
          <p>A <strong>service</strong> runs N identical replica containers behind one address; Envoy load-balances across them.</p>
          <p className="doc-h">Create one</p>
          <ol>
            <li><strong>New service</strong> → <em>Basic</em>: name, container port, network.</li>
            <li><em>Source</em>: a <strong>Docker image</strong>, or a <strong>Git repo</strong> (a push webhook then rebuilds &amp; rolls it out).</li>
            <li><em>Scaling</em>: replica count, or autoscale on CPU between min/max.</li>
            <li><em>Advanced</em>: environment variables, extra ports, volume mounts (searchable picker), command, healthcheck, and more.</li>
          </ol>
          <p className="doc-h">Addresses</p>
          <ul>
            <li><strong>Automatic host</strong> — always present: <code>&lt;service&gt;.&lt;server&gt;.&lt;domain&gt;</code>.</li>
            <li><strong>Custom subdomain</strong> — optional: <code>&lt;subdomain&gt;.&lt;domain&gt;</code>, set at create or later with the ✏️ on the card. It's additive.</li>
          </ul>
          <p className="doc-h">Manage</p>
          <ul>
            <li>Scale with the replica stepper; <em>Redeploy</em> (or Build &amp; redeploy for git); <em>Edit</em> to change any setting; delete.</li>
            <li><strong>Click a card's name</strong> to see its structure: domains → load balancer → replica containers (click a replica for its detail).</li>
          </ul>
          <Go tab="services" label="Open Services" />
        </>
      ),
    },
    {
      id: "requests",
      title: "Resource requests & quotas",
      icon: Icon.Inbox,
      body: (
        <>
          <p>Members need a granted quota before they can deploy. Quotas are <strong>per environment</strong>.</p>
          <ol>
            <li>On <strong>Requests</strong>, pick the <em>environment</em> and the CPU/RAM you need, add a reason, and submit.</li>
            <li>An admin approves it, which grants that quota <em>on that host</em>.</li>
            <li>You then create services within the quota — deploys that would exceed it are blocked.</li>
          </ol>
          <DocTip>Admins have no quota. Members can request more at any time.</DocTip>
          <Go tab="requests" label="Open Requests" />
        </>
      ),
    },
    {
      id: "routes",
      title: "Routes",
      icon: Icon.Route,
      adminOnly: true,
      body: (
        <>
          <p>Manually map a subdomain to an existing container/host and port — a lightweight alternative to a full service.</p>
          <Go tab="routes" label="Open Routes" />
        </>
      ),
    },
    {
      id: "networks",
      title: "Networks & Volumes",
      icon: Icon.Drive,
      body: (
        <>
          <p><strong>Networks</strong>: create/remove Docker networks (services default to the <code>easydeploy-edge</code> network so Envoy can reach them).</p>
          <p><strong>Volumes</strong>: create, delete (with force for in-use), see size &amp; usage, and a full <em>file manager</em> — browse, make folders, upload, download, delete.</p>
          <DocTip>Members can create and manage their own networks and volumes; you only see the ones you created. Admins see everything.</DocTip>
          <p>
            <Go tab="networks" label="Open Networks" /> &nbsp; <Go tab="volumes" label="Open Volumes" />
          </p>
        </>
      ),
    },
    {
      id: "registries",
      title: "Private registries",
      icon: Icon.Registry,
      body: (
        <>
          <p>Store registry credentials (encrypted at rest) so pulls from private registries authenticate automatically. You can test a login and browse repos/tags.</p>
          <DocTip>Members manage their own registries; you only see the ones you added.</DocTip>
          <Go tab="registries" label="Open Registries" />
        </>
      ),
    },
    {
      id: "environments",
      title: "Environments (multiple hosts)",
      icon: Icon.Server,
      adminOnly: true,
      body: (
        <>
          <p>Manage several Docker hosts from one place.</p>
          <ol>
            <li><strong>New environment</strong> → pick a connection: <strong>SSH</strong> (recommended — tunnels over SSH, no open port; set an SSH port if not 22), <strong>TLS</strong> (TCP + client certs), or <strong>Plain</strong> (trusted networks only). Enter a name and Docker host.</li>
            <li>Switch hosts with the top-right switcher; <em>Edit</em> or remove a host from its card.</li>
            <li>To run <strong>Routes/Services on a remote host</strong>, deploy its <strong>edge proxy</strong> from the card. This needs <code>EASYDEPLOY_XDS_ADVERTISE_ADDR</code> set to this machine's LAN address so the remote Envoy can reach the control plane.</li>
          </ol>
          <Go tab="environments" label="Open Environments" />
        </>
      ),
    },
    {
      id: "users",
      title: "Users & access",
      icon: Icon.Users,
      adminOnly: true,
      body: (
        <>
          <p>Create accounts and control what each can do.</p>
          <ul>
            <li><strong>Role</strong> (member/admin) — editable per row; the last admin can't be demoted.</li>
            <li><strong>Quota</strong> — set a member's local CPU/RAM inline (Save).</li>
            <li><strong>Reset password</strong> (🔑) and delete.</li>
            <li><strong>Assign environments</strong> (server icon) — grant a member specific remote hosts, each with its own per-environment quota.</li>
          </ul>
          <p>Members see and manage only their <em>own</em> containers, services, networks, volumes, and registries; they can't access images or hosts they weren't granted.</p>
          <Go tab="users" label="Open Users" />
        </>
      ),
    },
    {
      id: "expose",
      title: "Public exposure",
      icon: Icon.Globe,
      adminOnly: true,
      body: (
        <>
          <p>Make the local host's subdomains reachable from the internet, two ways:</p>
          <ul>
            <li><strong>WiFi / NAT public IP</strong> — detect your public IP and forward port 80.</li>
            <li><strong>Cloud VM SSH tunnel</strong> — a reverse tunnel so a cloud VM's public IP forwards traffic to your local Envoy, no router config.</li>
          </ul>
          <Go tab="expose" label="Open Expose" />
        </>
      ),
    },
  ];

  const shown = sections.filter((s) => isAdmin || !s.adminOnly);

  return (
    <div className="docs">
      <nav className="docs-toc">
        <div className="docs-toc-title">User guide</div>
        {shown.map((s) => (
          <button key={s.id} type="button" onClick={() => scrollTo(s.id)}>
            <s.icon size={15} /> <span>{s.title}</span>
          </button>
        ))}
      </nav>
      <div className="docs-content">
        {shown.map((s) => (
          <section key={s.id} className="doc-section" ref={(el) => (refs.current[s.id] = el)}>
            <h2>
              <s.icon size={18} /> {s.title}
            </h2>
            {s.body}
          </section>
        ))}
      </div>
    </div>
  );
}

function Err({ msg, onRetry }: { msg: string; onRetry?: () => void }) {
  return (
    <div className="error-panel">
      <span className="err-icon">
        <Icon.Alert size={22} />
      </span>
      <p className="err-title">Something went wrong</p>
      <p className="err-msg">{msg}</p>
      {onRetry && (
        <button type="button" className="err-retry" onClick={onRetry}>
          <Icon.Refresh size={14} /> <span>Try again</span>
        </button>
      )}
    </div>
  );
}

// EnvUnreachable replaces the tab content when the selected remote environment
// isn't responding, so the user gets a clear action instead of endless
// skeletons while requests hang on a dead host.
function EnvUnreachable({ onSwitch, onRetry }: { onSwitch: () => void; onRetry: () => void }) {
  return (
    <div className="error-panel">
      <span className="err-icon">
        <Icon.Server size={22} />
      </span>
      <p className="err-title">Environment unreachable</p>
      <p className="err-msg">
        This remote host isn’t responding — it may be offline, or the SSH/TCP connection is blocked. Switch to the local
        host, or retry once it’s back.
      </p>
      <div className="err-actions">
        <button type="button" className="primary" onClick={onSwitch}>
          <Icon.Server size={14} /> <span>Switch to Local</span>
        </button>
        <button type="button" onClick={onRetry}>
          <Icon.Refresh size={14} /> <span>Retry</span>
        </button>
      </div>
    </div>
  );
}
