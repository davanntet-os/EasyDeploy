// Typed client for the EasyDeploy REST + WebSocket API. All paths are
// relative so the same bundle works behind the Vite dev proxy and when
// served directly by the Go server.

import { readRoute, setRouteEnv, onRouteChange } from "./route";

export interface Container {
  Id: string;
  Names: string[];
  Image: string;
  State: string;
  Status: string;
  Labels: Record<string, string>;
  Ports: { PrivatePort: number; PublicPort?: number; Type: string }[];
}

export interface Route {
  id: number;
  subdomain: string;
  containerId: string;
  targetHost: string;
  targetPort: number;
  createdAt: string;
}

export interface Tunnel {
  id: number;
  kind: "wifi" | "ssh";
  name: string;
  sshHost: string;
  sshPort: number;
  sshUser: string;
  remotePort: number;
  localPort: number;
  enabled: boolean;
  running: boolean;
}

// ContainerInspect is the subset of Docker's inspect payload the edit form
// reads to prefill fields.
export interface ContainerInspect {
  Name: string;
  Config: {
    Image: string;
    Env: string[] | null;
    Labels: Record<string, string> | null;
    ExposedPorts?: Record<string, unknown>;
  };
  HostConfig: {
    PortBindings: Record<string, { HostIp?: string; HostPort: string }[]> | null;
    NanoCpus?: number;
    Memory?: number;
  };
  NetworkSettings: { Networks: Record<string, unknown> | null };
}

export interface DeployRequest {
  name: string;
  image: string;
  env: string[];
  subdomain: string;
  containerPort: number;
  publish: Record<string, string>;
  network: string;
  cpuMilli: number;
  memMB: number;
}

export type Role = "admin" | "member";

export interface Me {
  username: string;
  role: Role;
  cpuQuotaMilli: number;
  memQuotaMB: number;
  cpuUsedMilli: number;
  memUsedMB: number;
}

export interface User {
  id: number;
  username: string;
  role: Role;
  cpuQuotaMilli: number;
  memQuotaMB: number;
  createdAt: string;
}

export interface Service {
  id: number;
  name: string;
  owner: string;
  image: string;
  subdomain: string;
  containerPort: number;
  network: string;
  env: string; // JSON-encoded string[] as stored server-side
  cpuMilli: number;
  memMB: number;
  replicas: number;
  minReplicas: number;
  maxReplicas: number;
  autoscale: boolean;
  targetCpuPercent: number;
  gitRepo: string;
  gitBranch: string;
  gitDockerfile: string;
  webhookToken: string;
  lastImage: string;
  lastDeployAt: string | null;
  createdAt: string;
  // Host names this service is reachable at: the auto host
  // (<name>.<server>.<domain>) plus any custom <subdomain>.<domain>.
  domains: string[];
  // Parsed advanced options, so the edit form can round-trip them.
  advanced: AdvancedSpec;
}

export interface PortMap {
  hostIp: string;
  hostPort: string;
  containerPort: string;
  proto: string;
}
export interface VolumeMount {
  type: string; // bind | volume | tmpfs
  source: string;
  target: string;
  readOnly: boolean;
}
export interface HealthCheck {
  test: string[];
  intervalSec: number;
  timeoutSec: number;
  retries: number;
  startPeriodSec: number;
}
// Mirrors docker.AdvancedSpec — the full breadth of container options.
export interface AdvancedSpec {
  ports: PortMap[];
  mounts: VolumeMount[];
  command: string[];
  entrypoint: string[];
  workingDir: string;
  user: string;
  hostname: string;
  labels: Record<string, string>;
  extraHosts: string[];
  restartPolicy: string;
  restartRetries: number;
  capAdd: string[];
  capDrop: string[];
  privileged: boolean;
  readonlyRootfs: boolean;
  init: boolean;
  dns: string[];
  devices: string[];
  sysctls: Record<string, string>;
  tmpfs: Record<string, string>;
  pidsLimit: number;
  memorySwapMB: number;
  cpuShares: number;
  stopSignal: string;
  stopTimeoutSec: number;
  logDriver: string;
  logOpts: Record<string, string>;
  health: HealthCheck | null;
}

export interface ServiceRequest {
  name: string;
  image: string;
  subdomain: string;
  containerPort: number;
  network: string;
  env: string[];
  cpuMilli: number;
  memMB: number;
  replicas: number;
  minReplicas: number;
  maxReplicas: number;
  autoscale: boolean;
  targetCpuPercent: number;
  gitRepo: string;
  gitBranch: string;
  gitDockerfile: string;
  advanced: AdvancedSpec;
}

export interface ResourceRequest {
  id: number;
  userId: number;
  username: string;
  endpointId: number; // which environment the quota is for (0 = local)
  cpuMilli: number;
  memMB: number;
  note: string;
  status: "pending" | "approved" | "rejected";
  reviewedBy: string;
  reviewNote: string;
  createdAt: string;
  reviewedAt: string | null;
}

// EndpointGrant is a user's access to a remote environment plus its quota.
export interface EndpointGrant {
  endpointId: number;
  cpuQuotaMilli: number;
  memQuotaMB: number;
}

// --- token management ---
const TOKEN_KEY = "easydeploy_token";
export const auth = {
  get: () => localStorage.getItem(TOKEN_KEY) || "",
  set: (t: string) => localStorage.setItem(TOKEN_KEY, t),
  clear: () => localStorage.removeItem(TOKEN_KEY),
};

// onUnauthorized is invoked when the server rejects the session token, so the
// UI can drop back to the login screen.
let onUnauthorized: () => void = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

// The currently selected environment (Docker host). 0 = local. All requests
// carry it so container/image/volume/network operations target that host.
// The selection is mirrored into the URL hash (see route.ts) so it survives a
// refresh and can be shared.
let currentEndpointId = readRoute().env;
const envListeners = new Set<() => void>();
const notifyEnv = () => envListeners.forEach((l) => l());
export const environment = {
  get: () => currentEndpointId,
  set: (id: number) => {
    if (id === currentEndpointId) return;
    currentEndpointId = id;
    setRouteEnv(id); // reflect into the URL
    notifyEnv();
  },
  subscribe: (l: () => void): (() => void) => {
    envListeners.add(l);
    return () => {
      envListeners.delete(l);
    };
  },
};
// Keep the store in sync when the environment changes via the URL itself
// (back/forward navigation, or a pasted link).
onRouteChange(() => {
  const env = readRoute().env;
  if (env !== currentEndpointId) {
    currentEndpointId = env;
    notifyEnv();
  }
});

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const token = auth.get();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "X-Endpoint-Id": String(currentEndpointId),
    ...(init?.headers as Record<string, string> | undefined),
  };
  if (token) headers.Authorization = `Bearer ${token}`;
  const res = await fetch(`/api${path}`, { ...init, headers });
  if (res.status === 401) {
    // A 401 on a request that carried a token means the session expired or was
    // invalidated (e.g. the server restarted with a new signing key) — drop to
    // the login screen. A 401 without a token is a login/credential failure, so
    // surface the server's actual message instead.
    if (token) {
      auth.clear();
      onUnauthorized();
      throw new Error("session expired — please log in again");
    }
    const body = await res.json().catch(() => ({ error: "invalid credentials" }));
    throw new Error(body.error || "invalid credentials");
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || `HTTP ${res.status}`);
  }
  return res.status === 204 ? (undefined as T) : ((await res.json()) as T);
}

export interface Registry {
  id: number;
  name: string;
  url: string;
  username: string;
  createdAt: string;
}

export interface DockerNetwork {
  Id: string;
  Name: string;
  Driver: string;
  Scope: string;
  Containers?: Record<string, unknown>;
}

export interface Endpoint {
  id: number;
  name: string;
  host: string;
  tls: boolean;
  local: boolean;
}

// EdgeStatus is the state of a remote host's edge Envoy (routing data plane).
export interface EdgeStatus {
  present: boolean;
  running: boolean;
  hostPort: number;
  image: string;
}

export interface DockerVolume {
  name: string;
  driver: string;
  mountpoint: string;
  createdAt: string;
  labels: Record<string, string> | null;
  size: number; // bytes, -1 if unknown
  refCount: number; // containers using it, -1 if unknown
}

export interface VolFile {
  name: string;
  dir: boolean;
  size: number;
}

export const api = {
  login: (username: string, password: string) =>
    req<{ token: string; user: { username: string; role: Role } }>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  me: () => req<Me>("/me"),
  health: () => req<{ ok: boolean; dockerError: string }>("/health"),

  users: () => req<User[]>("/users"),
  createUser: (u: { username: string; password: string; role: Role; cpuQuotaMilli: number; memQuotaMB: number }) =>
    req<User>("/users", { method: "POST", body: JSON.stringify(u) }),
  updateUserQuota: (id: number, cpuQuotaMilli: number, memQuotaMB: number) =>
    req(`/users/${id}/quota`, { method: "PUT", body: JSON.stringify({ cpuQuotaMilli, memQuotaMB }) }),
  setUserRole: (id: number, role: Role) => req(`/users/${id}/role`, { method: "PUT", body: JSON.stringify({ role }) }),
  getUserEnvironments: (id: number) => req<EndpointGrant[]>(`/users/${id}/environments`),
  setUserEnvironments: (id: number, grants: EndpointGrant[]) =>
    req(`/users/${id}/environments`, { method: "PUT", body: JSON.stringify({ grants }) }),
  resetPassword: (id: number, password: string) =>
    req(`/users/${id}/password`, { method: "PUT", body: JSON.stringify({ password }) }),
  deleteUser: (id: number) => req(`/users/${id}`, { method: "DELETE" }),

  services: () => req<Service[]>("/services"),
  createService: (s: ServiceRequest) => req<Service>("/services", { method: "POST", body: JSON.stringify(s) }),
  updateService: (name: string, s: ServiceRequest) =>
    req<Service>(`/services/${name}`, { method: "PUT", body: JSON.stringify(s) }),
  scaleService: (name: string, replicas: number) =>
    req(`/services/${name}/scale`, { method: "POST", body: JSON.stringify({ replicas }) }),
  redeployService: (name: string, image = "") =>
    req(`/services/${name}/redeploy`, { method: "POST", body: JSON.stringify({ image }) }),
  setServiceSubdomain: (name: string, subdomain: string) =>
    req<Service>(`/services/${name}/subdomain`, { method: "POST", body: JSON.stringify({ subdomain }) }),
  deleteService: (name: string) => req(`/services/${name}`, { method: "DELETE" }),

  requests: (status?: string) => req<ResourceRequest[]>(`/requests${status ? `?status=${status}` : ""}`),
  createRequest: (r: { endpointId: number; cpuMilli: number; memMB: number; note: string }) =>
    req<ResourceRequest>("/requests", { method: "POST", body: JSON.stringify(r) }),
  reviewRequest: (id: number, body: { approve: boolean; grantCpuMilli?: number; grantMemMB?: number; note?: string }) =>
    req(`/requests/${id}/review`, { method: "POST", body: JSON.stringify(body) }),

  containers: () => req<Container[]>("/containers"),
  inspect: (id: string) => req<ContainerInspect>(`/containers/${id}`),
  edit: (id: string, body: DeployRequest) =>
    req<{ containerId: string }>(`/containers/${id}`, { method: "PUT", body: JSON.stringify(body) }),
  start: (id: string) => req(`/containers/${id}/start`, { method: "POST" }),
  stop: (id: string) => req(`/containers/${id}/stop`, { method: "POST" }),
  restart: (id: string) => req(`/containers/${id}/restart`, { method: "POST" }),
  update: (id: string, image = "") =>
    req<{ containerId: string }>(`/containers/${id}/update`, { method: "POST", body: JSON.stringify({ image }) }),
  remove: (id: string) => req(`/containers/${id}`, { method: "DELETE" }),

  images: () => req<unknown[]>("/images"),
  removeImage: (id: string) => req(`/images/${encodeURIComponent(id)}?force=true`, { method: "DELETE" }),

  networks: () => req<DockerNetwork[]>("/networks"),
  createNetwork: (name: string, driver = "bridge") =>
    req<{ id: string }>("/networks", { method: "POST", body: JSON.stringify({ name, driver }) }),
  removeNetwork: (id: string) => req(`/networks/${id}`, { method: "DELETE" }),

  endpoints: () => req<Endpoint[]>("/endpoints"),
  createEndpoint: (b: { name: string; host: string; tlsCa?: string; tlsCert?: string; tlsKey?: string }) =>
    req<Endpoint>("/endpoints", { method: "POST", body: JSON.stringify(b) }),
  updateEndpoint: (id: number, b: { name: string; host: string; tlsCa?: string; tlsCert?: string; tlsKey?: string }) =>
    req<Endpoint>(`/endpoints/${id}`, { method: "PUT", body: JSON.stringify(b) }),
  endpointStatus: (id: number) => req<{ ok: boolean; version: string }>(`/endpoints/${id}/status`),
  deleteEndpoint: (id: number) => req(`/endpoints/${id}`, { method: "DELETE" }),
  // Edge proxy: an Envoy on a remote host so Routes/Services work there.
  edgeStatus: (id: number) => req<EdgeStatus>(`/endpoints/${id}/edge`),
  deployEdge: (id: number, hostPort?: number) =>
    req<EdgeStatus>(`/endpoints/${id}/edge`, { method: "POST", body: JSON.stringify({ hostPort: hostPort ?? 0 }) }),
  removeEdge: (id: number) => req(`/endpoints/${id}/edge`, { method: "DELETE" }),

  volumes: () => req<DockerVolume[]>("/volumes"),
  // Volume names only — available to any authenticated user (the full volume
  // manager is admin-only), for the service form's mount-source picker.
  volumeNames: () => req<string[]>("/volume-names"),
  volumeUsage: () => req<Record<string, { size: number; refCount: number }>>("/volumes/usage"),
  createVolume: (name: string, driver = "local") =>
    req("/volumes", { method: "POST", body: JSON.stringify({ name, driver }) }),
  removeVolume: (name: string, force = false) =>
    req(`/volumes/${encodeURIComponent(name)}${force ? "?force=true" : ""}`, { method: "DELETE" }),
  browseVolume: (name: string, path: string) =>
    req<{ files: VolFile[] }>(`/volumes/${encodeURIComponent(name)}/browse?path=${encodeURIComponent(path)}`),
  mkdirVolume: (name: string, path: string) =>
    req(`/volumes/${encodeURIComponent(name)}/mkdir`, { method: "POST", body: JSON.stringify({ path }) }),
  deleteVolumeFile: (name: string, path: string) =>
    req(`/volumes/${encodeURIComponent(name)}/file?path=${encodeURIComponent(path)}`, { method: "DELETE" }),
  // Uploads via XMLHttpRequest (not fetch) so we can report upload progress.
  // onProgress receives a 0..1 fraction, or -1 once the bytes are all sent and
  // we're waiting on the server (helper container writes the file).
  uploadVolumeFile: (
    name: string,
    path: string,
    file: File,
    onProgress?: (fraction: number) => void,
  ): Promise<{ status: string; name: string }> => {
    return new Promise((resolve, reject) => {
      const fd = new FormData();
      fd.append("file", file);
      const xhr = new XMLHttpRequest();
      xhr.open("POST", `/api/volumes/${encodeURIComponent(name)}/upload?path=${encodeURIComponent(path)}`);
      // Must carry the target environment, otherwise the upload lands on the
      // local host while browse reads the remote (file appears to vanish).
      xhr.setRequestHeader("X-Endpoint-Id", String(currentEndpointId));
      const token = auth.get();
      if (token) xhr.setRequestHeader("Authorization", `Bearer ${token}`);
      xhr.upload.onprogress = (e) => {
        if (!onProgress) return;
        if (e.lengthComputable) onProgress(e.total ? e.loaded / e.total : 0);
      };
      // All bytes handed off; server is now writing the file to the volume.
      xhr.upload.onload = () => onProgress?.(-1);
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          try {
            resolve(JSON.parse(xhr.responseText));
          } catch {
            resolve({ status: "uploaded", name: file.name });
          }
        } else {
          let msg = `HTTP ${xhr.status}`;
          try {
            msg = JSON.parse(xhr.responseText).error || msg;
          } catch {
            /* keep default */
          }
          reject(new Error(msg));
        }
      };
      xhr.onerror = () => reject(new Error("network error"));
      xhr.send(fd);
    });
  },
  // A download URL carrying the token as a query param (browsers can't set
  // Authorization headers on a plain link).
  downloadVolumeURL: (name: string, path: string) =>
    `/api/volumes/${encodeURIComponent(name)}/download?path=${encodeURIComponent(path)}&token=${encodeURIComponent(auth.get())}&endpoint=${currentEndpointId}`,

  registries: () => req<Registry[]>("/registries"),
  createRegistry: (r: { name: string; url: string; username: string; password: string }) =>
    req<Registry>("/registries", { method: "POST", body: JSON.stringify(r) }),
  testRegistry: (r: { url: string; username: string; password: string }) =>
    req<{ status: string }>("/registries/test", { method: "POST", body: JSON.stringify(r) }),
  deleteRegistry: (id: number) => req(`/registries/${id}`, { method: "DELETE" }),
  registryCatalog: (id: number) => req<{ repositories: string[] }>(`/registries/${id}/catalog`),
  registryTags: (id: number, repo: string) =>
    req<{ tags: string[] }>(`/registries/${id}/tags?repo=${encodeURIComponent(repo)}`),

  routes: () => req<Route[]>("/routes"),
  upsertRoute: (r: Partial<Route>) =>
    req<Route>("/routes", { method: "POST", body: JSON.stringify(r) }),
  deleteRoute: (subdomain: string) =>
    req(`/routes/${subdomain}`, { method: "DELETE" }),

  publicIP: () => req<{ ip: string }>("/public-ip"),
  tunnels: () => req<Tunnel[]>("/tunnels"),
  createTunnel: (t: Partial<Tunnel>) =>
    req<Tunnel>("/tunnels", { method: "POST", body: JSON.stringify(t) }),
  startTunnel: (id: number) => req(`/tunnels/${id}/start`, { method: "POST" }),
  stopTunnel: (id: number) => req(`/tunnels/${id}/stop`, { method: "POST" }),
  deleteTunnel: (id: number) => req(`/tunnels/${id}`, { method: "DELETE" }),
};

// wsURL builds an absolute ws:// URL for a streaming endpoint from the
// current page origin, appending the session token as a query parameter
// (browsers cannot set Authorization headers on WebSocket connections).
export function wsURL(path: string): string {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const sep = path.includes("?") ? "&" : "?";
  return `${proto}://${location.host}/api${path}${sep}token=${encodeURIComponent(auth.get())}&endpoint=${currentEndpointId}`;
}
