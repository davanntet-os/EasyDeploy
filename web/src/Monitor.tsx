import { useEffect, useRef, useState } from "react";
import { wsURL, type Container } from "./api";
import { Icon } from "./icons";

// Raw Docker stats shape (only the fields we read).
interface RawStats {
  cpu_stats: CPU;
  precpu_stats: CPU;
  memory_stats: { usage?: number; limit?: number; stats?: { cache?: number; inactive_file?: number } };
  networks?: Record<string, { rx_bytes: number; tx_bytes: number }>;
}
interface CPU {
  cpu_usage: { total_usage: number };
  system_cpu_usage?: number;
  online_cpus?: number;
}

interface Sample {
  cpuPct: number;
  memUsed: number; // bytes
  memLimit: number; // bytes
  memPct: number;
  rxRate: number; // bytes/sec
  txRate: number;
}

const HISTORY = 40;

function computeCPU(s: RawStats): number {
  const cpuDelta = s.cpu_stats.cpu_usage.total_usage - s.precpu_stats.cpu_usage.total_usage;
  const sysDelta = (s.cpu_stats.system_cpu_usage || 0) - (s.precpu_stats.system_cpu_usage || 0);
  const cpus = s.cpu_stats.online_cpus || 1;
  if (sysDelta <= 0 || cpuDelta < 0) return 0;
  return Math.min(100 * cpus, (cpuDelta / sysDelta) * cpus * 100);
}

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

// Status color by threshold — the numeric value is always shown alongside, so
// color is a secondary cue (accessibility), never the only signal.
function meterClass(pct: number): string {
  if (pct >= 90) return "critical";
  if (pct >= 70) return "warning";
  return "ok";
}

export function Monitor({ container, onClose, embedded }: { container: Container; onClose?: () => void; embedded?: boolean }) {
  const name = container.Names?.[0]?.replace(/^\//, "") || container.Id.slice(0, 12);
  const [sample, setSample] = useState<Sample | null>(null);
  const [cpuHist, setCpuHist] = useState<number[]>([]);
  const [memHist, setMemHist] = useState<number[]>([]);
  const prevNet = useRef<{ rx: number; tx: number; t: number } | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    // React StrictMode mounts effects twice in dev; guard so closing the first
    // socket doesn't surface as a user-facing error while the second streams.
    let closed = false;
    const ws = new WebSocket(wsURL(`/containers/${container.Id}/stats`));
    ws.onmessage = (e) => {
      let raw: RawStats;
      try {
        raw = JSON.parse(e.data as string);
      } catch {
        return;
      }
      if (!raw.cpu_stats) return;
      setErr(""); // a sample arrived — clear any stale error

      const cpuPct = computeCPU(raw);
      const cache = raw.memory_stats.stats?.inactive_file ?? raw.memory_stats.stats?.cache ?? 0;
      const memUsed = Math.max(0, (raw.memory_stats.usage || 0) - cache);
      const memLimit = raw.memory_stats.limit || 0;
      const memPct = memLimit ? (memUsed / memLimit) * 100 : 0;

      let rx = 0;
      let tx = 0;
      for (const n of Object.values(raw.networks || {})) {
        rx += n.rx_bytes;
        tx += n.tx_bytes;
      }
      const now = Date.now();
      let rxRate = 0;
      let txRate = 0;
      if (prevNet.current) {
        const dt = (now - prevNet.current.t) / 1000 || 1;
        rxRate = Math.max(0, (rx - prevNet.current.rx) / dt);
        txRate = Math.max(0, (tx - prevNet.current.tx) / dt);
      }
      prevNet.current = { rx, tx, t: now };

      setSample({ cpuPct, memUsed, memLimit, memPct, rxRate, txRate });
      setCpuHist((h) => [...h.slice(-HISTORY + 1), cpuPct]);
      setMemHist((h) => [...h.slice(-HISTORY + 1), memPct]);
    };
    ws.onerror = () => {
      if (!closed) setErr("stats stream error");
    };
    return () => {
      closed = true;
      ws.close();
    };
  }, [container.Id]);

  const body = (
    <div className="mon-body">
      {err && <p className="error">{err}</p>}
      {!sample ? (
        <div className="loading">
          <Icon.Spinner size={20} /> <span>Waiting for first sample…</span>
        </div>
      ) : (
        <>
          <div className="mon-grid">
            <MetricCard label="CPU" value={`${sample.cpuPct.toFixed(1)}%`} pct={sample.cpuPct} hist={cpuHist} max={100} />
            <MetricCard
              label="Memory"
              value={`${fmtBytes(sample.memUsed)} / ${fmtBytes(sample.memLimit)}`}
              sub={`${sample.memPct.toFixed(1)}%`}
              pct={sample.memPct}
              hist={memHist}
              max={100}
            />
          </div>
          <div className="mon-io">
            <IORow label="Network in" value={`${fmtBytes(sample.rxRate)}/s`} icon="down" />
            <IORow label="Network out" value={`${fmtBytes(sample.txRate)}/s`} icon="up" />
          </div>
        </>
      )}
    </div>
  );

  if (embedded) return body;

  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body wide" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong>
            <Icon.Activity size={15} /> {name} — monitoring
          </strong>
          <button type="button" onClick={onClose} aria-label="Close">
            <Icon.Close size={16} />
          </button>
        </div>
        {body}
      </div>
    </div>
  );
}

function MetricCard({
  label,
  value,
  sub,
  pct,
  hist,
  max,
}: {
  label: string;
  value: string;
  sub?: string;
  pct: number;
  hist: number[];
  max: number;
}) {
  const cls = meterClass(pct);
  return (
    <div className="metric-card">
      <div className="metric-head">
        <span className="muted">{label}</span>
        <span className="metric-value">
          {value}
          {sub && <span className="muted metric-sub"> · {sub}</span>}
        </span>
      </div>
      <div className="meter">
        <span className={`meter-fill ${cls}`} style={{ width: `${Math.min(100, pct)}%` }} />
      </div>
      <Sparkline data={hist} max={max} className={cls} />
    </div>
  );
}

// Sparkline: a single 2px recessive line, baseline-anchored, no gridlines or
// axes — it shows the shape of change, the number above shows the value.
function Sparkline({ data, max, className }: { data: number[]; max: number; className: string }) {
  const W = 260;
  const H = 44;
  if (data.length < 2) return <svg className={`sparkline ${className}`} viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" />;
  const step = W / (HISTORY - 1);
  const pts = data.map((v, i) => {
    const x = i * step;
    const y = H - (Math.min(v, max) / max) * (H - 4) - 2;
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });
  const area = `0,${H} ${pts.join(" ")} ${(data.length - 1) * step},${H}`;
  return (
    <svg className={`sparkline ${className}`} viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" aria-hidden="true">
      <polygon className="spark-area" points={area} />
      <polyline className="spark-line" points={pts.join(" ")} />
    </svg>
  );
}

function IORow({ label, value, icon }: { label: string; value: string; icon: "up" | "down" }) {
  return (
    <div className="io-row">
      <span className="muted io-label">
        <span className={`io-arrow ${icon}`}>{icon === "down" ? "↓" : "↑"}</span>
        {label}
      </span>
      <span className="mono">{value}</span>
    </div>
  );
}
