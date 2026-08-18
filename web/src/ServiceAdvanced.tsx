import { useEffect, useState } from "react";
import { Icon } from "./icons";
import { Field, SearchPicker } from "./FormKit";
import { api, type AdvancedSpec, type PortMap, type VolumeMount } from "./api";

// AdvForm is the edit-friendly shape: lists are entered as text (one per line
// or comma-separated) and converted to the API's AdvancedSpec on submit.
export interface AdvForm {
  ports: PortMap[];
  mounts: VolumeMount[];
  command: string;
  entrypoint: string;
  workingDir: string;
  user: string;
  hostname: string;
  restartPolicy: string;
  restartRetries: number;
  privileged: boolean;
  readonlyRootfs: boolean;
  init: boolean;
  capAdd: string;
  capDrop: string;
  extraHosts: string;
  dns: string;
  devices: string;
  labels: string;
  sysctls: string;
  tmpfs: string;
  pidsLimit: number;
  memorySwapMB: number;
  cpuShares: number;
  stopSignal: string;
  stopTimeoutSec: number;
  logDriver: string;
  healthTest: string;
  healthIntervalSec: number;
  healthTimeoutSec: number;
  healthRetries: number;
}

export function emptyAdvForm(): AdvForm {
  return {
    ports: [],
    mounts: [],
    command: "",
    entrypoint: "",
    workingDir: "",
    user: "",
    hostname: "",
    restartPolicy: "unless-stopped",
    restartRetries: 0,
    privileged: false,
    readonlyRootfs: false,
    init: false,
    capAdd: "",
    capDrop: "",
    extraHosts: "",
    dns: "",
    devices: "",
    labels: "",
    sysctls: "",
    tmpfs: "",
    pidsLimit: 0,
    memorySwapMB: 0,
    cpuShares: 0,
    stopSignal: "",
    stopTimeoutSec: 0,
    logDriver: "",
    healthTest: "",
    healthIntervalSec: 0,
    healthTimeoutSec: 0,
    healthRetries: 0,
  };
}

const lines = (s: string) => s.split("\n").map((l) => l.trim()).filter(Boolean);
const tokens = (s: string) => s.split(/[\s,]+/).filter(Boolean);
const pairs = (s: string): Record<string, string> => {
  const out: Record<string, string> = {};
  for (const line of lines(s)) {
    const eq = line.indexOf("=");
    if (eq > 0) out[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
  }
  return out;
};

// buildAdvanced converts the edit form into the API AdvancedSpec.
export function buildAdvanced(f: AdvForm): AdvancedSpec {
  return {
    ports: f.ports.filter((p) => p.containerPort.trim() !== ""),
    mounts: f.mounts.filter((m) => m.target.trim() !== ""),
    command: lines(f.command),
    entrypoint: lines(f.entrypoint),
    workingDir: f.workingDir,
    user: f.user,
    hostname: f.hostname,
    labels: pairs(f.labels),
    extraHosts: lines(f.extraHosts),
    restartPolicy: f.restartPolicy,
    restartRetries: Number(f.restartRetries) || 0,
    capAdd: tokens(f.capAdd),
    capDrop: tokens(f.capDrop),
    privileged: f.privileged,
    readonlyRootfs: f.readonlyRootfs,
    init: f.init,
    dns: lines(f.dns),
    devices: lines(f.devices),
    sysctls: pairs(f.sysctls),
    tmpfs: pairs(f.tmpfs),
    pidsLimit: Number(f.pidsLimit) || 0,
    memorySwapMB: Number(f.memorySwapMB) || 0,
    cpuShares: Number(f.cpuShares) || 0,
    stopSignal: f.stopSignal,
    stopTimeoutSec: Number(f.stopTimeoutSec) || 0,
    logDriver: f.logDriver,
    logOpts: {},
    health: f.healthTest.trim()
      ? {
          test: ["CMD-SHELL", f.healthTest],
          intervalSec: Number(f.healthIntervalSec) || 0,
          timeoutSec: Number(f.healthTimeoutSec) || 0,
          retries: Number(f.healthRetries) || 0,
          startPeriodSec: 0,
        }
      : null,
  };
}

const joinLines = (a?: string[]) => (a ?? []).join("\n");
const joinToks = (a?: string[]) => (a ?? []).join(" ");
const joinPairs = (m?: Record<string, string>) =>
  Object.entries(m ?? {})
    .map(([k, v]) => `${k}=${v}`)
    .join("\n");

// advToForm is the inverse of buildAdvanced: it turns a stored AdvancedSpec back
// into the edit-friendly form shape so an existing service can be edited.
export function advToForm(s?: AdvancedSpec): AdvForm {
  const base = emptyAdvForm();
  if (!s) return base;
  return {
    ...base,
    ports: s.ports ?? [],
    mounts: s.mounts ?? [],
    command: joinLines(s.command),
    entrypoint: joinLines(s.entrypoint),
    workingDir: s.workingDir ?? "",
    user: s.user ?? "",
    hostname: s.hostname ?? "",
    restartPolicy: s.restartPolicy || "unless-stopped",
    restartRetries: s.restartRetries ?? 0,
    privileged: !!s.privileged,
    readonlyRootfs: !!s.readonlyRootfs,
    init: !!s.init,
    capAdd: joinToks(s.capAdd),
    capDrop: joinToks(s.capDrop),
    extraHosts: joinLines(s.extraHosts),
    dns: joinLines(s.dns),
    devices: joinLines(s.devices),
    labels: joinPairs(s.labels),
    sysctls: joinPairs(s.sysctls),
    tmpfs: joinPairs(s.tmpfs),
    pidsLimit: s.pidsLimit ?? 0,
    memorySwapMB: s.memorySwapMB ?? 0,
    cpuShares: s.cpuShares ?? 0,
    stopSignal: s.stopSignal ?? "",
    stopTimeoutSec: s.stopTimeoutSec ?? 0,
    logDriver: s.logDriver ?? "",
    healthTest: s.health?.test?.[s.health.test.length - 1] ?? "",
    healthIntervalSec: s.health?.intervalSec ?? 0,
    healthTimeoutSec: s.health?.timeoutSec ?? 0,
    healthRetries: s.health?.retries ?? 0,
  };
}

// Group is a labeled sub-section within the advanced panel.
function Group({ icon: I, title, children }: { readonly icon: (p: { size?: number }) => JSX.Element; readonly title: string; readonly children: React.ReactNode }) {
  return (
    <div className="adv-group">
      <div className="adv-group-head">
        <I size={13} />
        {title}
      </div>
      {children}
    </div>
  );
}

export function AdvancedPanel({
  form,
  onChange,
  canPickVolumes = true,
}: {
  readonly form: AdvForm;
  readonly onChange: (f: AdvForm) => void;
  readonly canPickVolumes?: boolean;
}) {
  const set = <K extends keyof AdvForm>(k: K, v: AdvForm[K]) => onChange({ ...form, [k]: v });
  const upd = (k: "ports" | "mounts", i: number, patch: object) => updateItem(form, onChange, k, i, patch as never);
  const rm = (k: "ports" | "mounts", i: number) => removeItem(form, onChange, k, i);

  // Existing volumes on the selected host, offered as a searchable dropdown for
  // "volume" mount sources. Volume listing is admin-only, so members skip it
  // (they type the name) — canPickVolumes is false for them.
  const [volumes, setVolumes] = useState<string[]>([]);
  useEffect(() => {
    if (!canPickVolumes) return;
    let live = true;
    api.volumeNames().then((vs) => live && setVolumes(vs ?? [])).catch(() => {});
    return () => {
      live = false;
    };
  }, [canPickVolumes]);

  return (
    <div className="adv-panel">
      {/* Networking */}
      <Group icon={Icon.Network} title="Networking">
        <div className="adv-sub">
          <span className="adv-sub-label">Published ports</span>
          {form.ports.map((p, i) => (
            <div className="adv-row" key={`p${i}`}>
              <input placeholder="host" value={p.hostPort} onChange={(e) => upd("ports", i, { hostPort: e.target.value })} />
              <span className="adv-sep">:</span>
              <input placeholder="container" value={p.containerPort} onChange={(e) => upd("ports", i, { containerPort: e.target.value })} />
              <select value={p.proto || "tcp"} onChange={(e) => upd("ports", i, { proto: e.target.value })}>
                <option value="tcp">tcp</option>
                <option value="udp">udp</option>
              </select>
              <button type="button" className="btn-icon danger" onClick={() => rm("ports", i)}>
                <Icon.Minus size={14} />
              </button>
            </div>
          ))}
          <button type="button" className="adv-add" onClick={() => set("ports", [...form.ports, { hostIp: "", hostPort: "", containerPort: "", proto: "tcp" }])}>
            <Icon.Plus size={14} /> <span>Add port</span>
          </button>
        </div>
        <div className="row">
          <Field label="Extra hosts" hint="host:ip per line">
            <textarea rows={2} value={form.extraHosts} onChange={(e) => set("extraHosts", e.target.value)} />
          </Field>
          <Field label="DNS servers" hint="one per line">
            <textarea rows={2} value={form.dns} onChange={(e) => set("dns", e.target.value)} />
          </Field>
        </div>
      </Group>

      {/* Storage */}
      <Group icon={Icon.Box} title="Storage">
        <div className="adv-sub">
          <span className="adv-sub-label">Volumes & mounts</span>
          {form.mounts.map((m, i) => (
            <div className="adv-row" key={`m${i}`}>
              <select value={m.type || "volume"} onChange={(e) => upd("mounts", i, { type: e.target.value })}>
                <option value="volume">volume</option>
                <option value="bind">bind</option>
                <option value="tmpfs">tmpfs</option>
              </select>
              {m.type === "volume" ? (
                <SearchPicker value={m.source} options={volumes} onChange={(v) => upd("mounts", i, { source: v })} placeholder="pick or type a volume" icon={Icon.Drive} />
              ) : (
                <input
                  placeholder={m.type === "tmpfs" ? "(none)" : "host path"}
                  value={m.source}
                  disabled={m.type === "tmpfs"}
                  onChange={(e) => upd("mounts", i, { source: e.target.value })}
                />
              )}
              <span className="adv-sep">→</span>
              <input placeholder="container path" value={m.target} onChange={(e) => upd("mounts", i, { target: e.target.value })} />
              <label className="adv-check">
                <input type="checkbox" checked={m.readOnly} onChange={(e) => upd("mounts", i, { readOnly: e.target.checked })} /> ro
              </label>
              <button type="button" className="btn-icon danger" onClick={() => rm("mounts", i)}>
                <Icon.Minus size={14} />
              </button>
            </div>
          ))}
          <button type="button" className="adv-add" onClick={() => set("mounts", [...form.mounts, { type: "volume", source: "", target: "", readOnly: false }])}>
            <Icon.Plus size={14} /> <span>Add mount</span>
          </button>
        </div>
        <div className="row">
          <Field label="tmpfs" hint="PATH=options per line">
            <textarea rows={2} value={form.tmpfs} onChange={(e) => set("tmpfs", e.target.value)} />
          </Field>
          <Field label="Devices" hint="host:container per line">
            <textarea rows={2} value={form.devices} onChange={(e) => set("devices", e.target.value)} />
          </Field>
        </div>
      </Group>

      {/* Command & runtime */}
      <Group icon={Icon.Terminal} title="Command & runtime">
        <div className="row">
          <Field label="Command" hint="one arg per line">
            <textarea rows={2} value={form.command} onChange={(e) => set("command", e.target.value)} />
          </Field>
          <Field label="Entrypoint" hint="one arg per line">
            <textarea rows={2} value={form.entrypoint} onChange={(e) => set("entrypoint", e.target.value)} />
          </Field>
        </div>
        <div className="row">
          <Field label="Working dir">
            <input value={form.workingDir} onChange={(e) => set("workingDir", e.target.value)} />
          </Field>
          <Field label="User" help="uid[:gid]">
            <input value={form.user} onChange={(e) => set("user", e.target.value)} />
          </Field>
          <Field label="Hostname">
            <input value={form.hostname} onChange={(e) => set("hostname", e.target.value)} />
          </Field>
        </div>
      </Group>

      {/* Lifecycle */}
      <Group icon={Icon.Restart} title="Lifecycle">
        <div className="row">
          <Field label="Restart policy">
            <select value={form.restartPolicy} onChange={(e) => set("restartPolicy", e.target.value)}>
              <option value="unless-stopped">unless-stopped</option>
              <option value="always">always</option>
              <option value="on-failure">on-failure</option>
              <option value="no">no</option>
            </select>
          </Field>
          {form.restartPolicy === "on-failure" && (
            <Field label="Max retries">
              <input type="number" min="0" value={form.restartRetries} onChange={(e) => set("restartRetries", Number(e.target.value))} />
            </Field>
          )}
          <Field label="Stop signal">
            <input placeholder="SIGTERM" value={form.stopSignal} onChange={(e) => set("stopSignal", e.target.value)} />
          </Field>
          <Field label="Stop timeout" hint="seconds">
            <input type="number" min="0" value={form.stopTimeoutSec} onChange={(e) => set("stopTimeoutSec", Number(e.target.value))} />
          </Field>
        </div>
      </Group>

      {/* Security */}
      <Group icon={Icon.Cpu} title="Security">
        <div className="adv-checks">
          <label className="check">
            <input type="checkbox" checked={form.privileged} onChange={(e) => set("privileged", e.target.checked)} /> <span>Privileged</span>
          </label>
          <label className="check">
            <input type="checkbox" checked={form.readonlyRootfs} onChange={(e) => set("readonlyRootfs", e.target.checked)} /> <span>Read-only rootfs</span>
          </label>
          <label className="check">
            <input type="checkbox" checked={form.init} onChange={(e) => set("init", e.target.checked)} /> <span>Init process</span>
          </label>
        </div>
        <div className="row">
          <Field label="Capabilities add" hint="comma / space">
            <input placeholder="NET_ADMIN SYS_TIME" value={form.capAdd} onChange={(e) => set("capAdd", e.target.value)} />
          </Field>
          <Field label="Capabilities drop">
            <input placeholder="ALL" value={form.capDrop} onChange={(e) => set("capDrop", e.target.value)} />
          </Field>
        </div>
      </Group>

      {/* Resources */}
      <Group icon={Icon.Activity} title="Resource limits">
        <div className="row">
          <Field label="PIDs limit">
            <input type="number" min="0" value={form.pidsLimit} onChange={(e) => set("pidsLimit", Number(e.target.value))} />
          </Field>
          <Field label="Memory + swap" hint="MB">
            <input type="number" min="0" value={form.memorySwapMB} onChange={(e) => set("memorySwapMB", Number(e.target.value))} />
          </Field>
          <Field label="CPU shares">
            <input type="number" min="0" value={form.cpuShares} onChange={(e) => set("cpuShares", Number(e.target.value))} />
          </Field>
        </div>
      </Group>

      {/* Metadata, logging & health */}
      <Group icon={Icon.Registry} title="Metadata, logging & health">
        <div className="row">
          <Field label="Labels" hint="KEY=VALUE per line">
            <textarea rows={2} value={form.labels} onChange={(e) => set("labels", e.target.value)} />
          </Field>
          <Field label="Sysctls" hint="KEY=VALUE per line">
            <textarea rows={2} value={form.sysctls} onChange={(e) => set("sysctls", e.target.value)} />
          </Field>
        </div>
        <Field label="Log driver">
          <input placeholder="json-file" value={form.logDriver} onChange={(e) => set("logDriver", e.target.value)} />
        </Field>
        <div className="adv-sub">
          <span className="adv-sub-label">Healthcheck</span>
          <Field label="Test command (shell)">
            <input placeholder="curl -f http://localhost/ || exit 1" value={form.healthTest} onChange={(e) => set("healthTest", e.target.value)} />
          </Field>
          {form.healthTest.trim() !== "" && (
            <div className="row">
              <Field label="Interval" hint="s">
                <input type="number" min="0" value={form.healthIntervalSec} onChange={(e) => set("healthIntervalSec", Number(e.target.value))} />
              </Field>
              <Field label="Timeout" hint="s">
                <input type="number" min="0" value={form.healthTimeoutSec} onChange={(e) => set("healthTimeoutSec", Number(e.target.value))} />
              </Field>
              <Field label="Retries">
                <input type="number" min="0" value={form.healthRetries} onChange={(e) => set("healthRetries", Number(e.target.value))} />
              </Field>
            </div>
          )}
        </div>
      </Group>
    </div>
  );
}

// updateItem/removeItem edit one element of a PortMap[] or VolumeMount[] field.
function updateItem<K extends "ports" | "mounts">(
  form: AdvForm,
  onChange: (f: AdvForm) => void,
  key: K,
  i: number,
  patch: Partial<AdvForm[K][number]>
) {
  const arr = [...form[key]];
  arr[i] = { ...arr[i], ...patch } as AdvForm[K][number];
  onChange({ ...form, [key]: arr });
}

function removeItem(form: AdvForm, onChange: (f: AdvForm) => void, key: "ports" | "mounts", i: number) {
  onChange({ ...form, [key]: form[key].filter((_, j) => j !== i) });
}
