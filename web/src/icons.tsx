// A small, consistent stroke-icon set (currentColor, 1.6 stroke) so the whole
// UI reads as one system. Each icon inherits font color and sizes to 1em by
// default; pass `size` to override.

type IconProps = { size?: number; className?: string };

function Svg({ size = 16, className, children }: IconProps & { children: React.ReactNode }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.6}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      {children}
    </svg>
  );
}

export const Icon = {
  // Logo — an original mark for EasyDeploy: a single source node at the base
  // fans up to three nodes, reading at once as "deploy upward" and
  // "load-balance / route to many". Pure geometry, drawn from scratch.
  Logo: ({ size = 20, className }: IconProps) => (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" className={className} aria-hidden="true">
      {/* fan-out branches from the source hub to the three nodes */}
      <path
        d="M12 17.2 L6.2 8.4 M12 17.2 L12 6 M12 17.2 L17.8 8.4"
        stroke="currentColor"
        strokeWidth="1.9"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      {/* three destination nodes (replicas / subdomains) */}
      <circle cx="6" cy="7" r="2.1" fill="currentColor" />
      <circle cx="12" cy="5" r="2.1" fill="currentColor" />
      <circle cx="18" cy="7" r="2.1" fill="currentColor" />
      {/* the source hub (the service being deployed) */}
      <circle cx="12" cy="18.5" r="2.7" fill="currentColor" />
    </svg>
  ),
  Play: (p: IconProps) => (
    <Svg {...p}>
      <polygon points="6 4 20 12 6 20 6 4" />
    </Svg>
  ),
  Stop: (p: IconProps) => (
    <Svg {...p}>
      <rect x="6" y="6" width="12" height="12" rx="1.5" />
    </Svg>
  ),
  Restart: (p: IconProps) => (
    <Svg {...p}>
      <path d="M3 12a9 9 0 1 0 3-6.7L3 8" />
      <path d="M3 3v5h5" />
    </Svg>
  ),
  Update: (p: IconProps) => (
    <Svg {...p}>
      <path d="M21 12a9 9 0 1 1-3-6.7" />
      <path d="M21 3v6h-6" />
    </Svg>
  ),
  Edit: (p: IconProps) => (
    <Svg {...p}>
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </Svg>
  ),
  Trash: (p: IconProps) => (
    <Svg {...p}>
      <path d="M3 6h18" />
      <path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2m2 0v14a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V6" />
      <path d="M10 11v6M14 11v6" />
    </Svg>
  ),
  Logs: (p: IconProps) => (
    <Svg {...p}>
      <path d="M4 6h16M4 10h16M4 14h11M4 18h14" />
    </Svg>
  ),
  Plus: (p: IconProps) => (
    <Svg {...p}>
      <path d="M12 5v14M5 12h14" />
    </Svg>
  ),
  Box: (p: IconProps) => (
    <Svg {...p}>
      <path d="M21 8l-9-5-9 5 9 5 9-5Z" />
      <path d="M3 8v8l9 5 9-5V8" />
      <path d="M12 13v8" />
    </Svg>
  ),
  Rocket: (p: IconProps) => (
    <Svg {...p}>
      <path d="M5 15c-1.5 1.3-2 5-2 5s3.7-.5 5-2" />
      <path d="M9 15l-3-3c1-5 5-9 12-9 0 7-4 11-9 12Z" />
      <circle cx="14.5" cy="9.5" r="1.5" />
    </Svg>
  ),
  Network: (p: IconProps) => (
    <Svg {...p}>
      <circle cx="12" cy="5" r="2.5" />
      <circle cx="5" cy="19" r="2.5" />
      <circle cx="19" cy="19" r="2.5" />
      <path d="M12 7.5v4M12 11.5l-5.5 5M12 11.5l5.5 5" />
    </Svg>
  ),
  Route: (p: IconProps) => (
    <Svg {...p}>
      <path d="M9 17H7A4 4 0 0 1 7 9h1" />
      <path d="M15 7h2a4 4 0 0 1 0 8h-1" />
      <path d="M8 13h8" />
    </Svg>
  ),
  Registry: (p: IconProps) => (
    <Svg {...p}>
      <ellipse cx="12" cy="5" rx="8" ry="3" />
      <path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5" />
      <path d="M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6" />
    </Svg>
  ),
  Globe: (p: IconProps) => (
    <Svg {...p}>
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18M12 3c2.5 2.5 3.8 5.7 3.8 9S14.5 18.5 12 21c-2.5-2.5-3.8-5.7-3.8-9S9.5 5.5 12 3Z" />
    </Svg>
  ),
  Check: (p: IconProps) => (
    <Svg {...p}>
      <path d="M20 6 9 17l-5-5" />
    </Svg>
  ),
  Alert: (p: IconProps) => (
    <Svg {...p}>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 8v4M12 16h.01" />
    </Svg>
  ),
  Close: (p: IconProps) => (
    <Svg {...p}>
      <path d="M18 6 6 18M6 6l12 12" />
    </Svg>
  ),
  Logout: (p: IconProps) => (
    <Svg {...p}>
      <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
      <path d="M16 17l5-5-5-5M21 12H9" />
    </Svg>
  ),
  Refresh: (p: IconProps) => (
    <Svg {...p}>
      <path d="M21 12a9 9 0 1 1-3-6.7" />
      <path d="M21 3v6h-6" />
    </Svg>
  ),
  Users: (p: IconProps) => (
    <Svg {...p}>
      <circle cx="9" cy="8" r="3.2" />
      <path d="M3.5 20a5.5 5.5 0 0 1 11 0" />
      <path d="M16 5.2a3.2 3.2 0 0 1 0 5.6M17 14.5a5.5 5.5 0 0 1 3.5 5.5" />
    </Svg>
  ),
  Inbox: (p: IconProps) => (
    <Svg {...p}>
      <path d="M4 13l2.5-8h11L20 13" />
      <path d="M4 13v5a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-5h-5a3 3 0 0 1-6 0H4Z" />
    </Svg>
  ),
  Layers: (p: IconProps) => (
    <Svg {...p}>
      <path d="M12 3 3 8l9 5 9-5-9-5Z" />
      <path d="M3 13l9 5 9-5M3 16.5l9 5 9-5" />
    </Svg>
  ),
  Git: (p: IconProps) => (
    <Svg {...p}>
      <circle cx="6" cy="6" r="2.5" />
      <circle cx="6" cy="18" r="2.5" />
      <circle cx="18" cy="9" r="2.5" />
      <path d="M6 8.5v7M18 11.2c0 3-3 3.3-6 3.3" />
    </Svg>
  ),
  Webhook: (p: IconProps) => (
    <Svg {...p}>
      <path d="M9 7a3 3 0 1 1 4 2.8l-2.5 4.2" />
      <path d="M15 12a3 3 0 1 1-2.8 4H7.5" />
      <path d="M6.5 12.5A3 3 0 1 1 10 17" />
    </Svg>
  ),
  Minus: (p: IconProps) => (
    <Svg {...p}>
      <path d="M5 12h14" />
    </Svg>
  ),
  Terminal: (p: IconProps) => (
    <Svg {...p}>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M7 9l3 3-3 3M13 15h4" />
    </Svg>
  ),
  Activity: (p: IconProps) => (
    <Svg {...p}>
      <path d="M3 12h4l2.5-7 5 14 2.5-7H21" />
    </Svg>
  ),
  Menu: (p: IconProps) => (
    <Svg {...p}>
      <path d="M3 6h18M3 12h18M3 18h18" />
    </Svg>
  ),
  Gauge: (p: IconProps) => (
    <Svg {...p}>
      <path d="M12 14a2 2 0 1 0 0-4 2 2 0 0 0 0 4Z" />
      <path d="M13.4 12.6 19 7" />
      <path d="M4 20a9 9 0 1 1 16 0" />
    </Svg>
  ),
  Play2: (p: IconProps) => (
    <Svg {...p}>
      <polygon points="6 4 20 12 6 20 6 4" />
    </Svg>
  ),
  Server: (p: IconProps) => (
    <Svg {...p}>
      <rect x="3" y="4" width="18" height="7" rx="1.5" />
      <rect x="3" y="13" width="18" height="7" rx="1.5" />
      <path d="M7 7.5h.01M7 16.5h.01" />
    </Svg>
  ),
  Chevron: (p: IconProps) => (
    <Svg {...p}>
      <path d="M6 9l6 6 6-6" />
    </Svg>
  ),
  Search: (p: IconProps) => (
    <Svg {...p}>
      <circle cx="11" cy="11" r="7" />
      <path d="M21 21l-4.35-4.35" />
    </Svg>
  ),
  Key: (p: IconProps) => (
    <Svg {...p}>
      <circle cx="7.5" cy="15.5" r="4" />
      <path d="M10.3 12.7L20 3M16.5 6.5l2.5 2.5M13.5 9.5l2.5 2.5" />
    </Svg>
  ),
  Book: (p: IconProps) => (
    <Svg {...p}>
      <path d="M4 5a2 2 0 0 1 2-2h13v16H6a2 2 0 0 0-2 2V5Z" />
      <path d="M4 19a2 2 0 0 1 2-2h13" />
    </Svg>
  ),
  Drive: (p: IconProps) => (
    <Svg {...p}>
      <rect x="3" y="5" width="18" height="14" rx="2" />
      <path d="M3 12h18" />
      <path d="M7 16h.01M11 16h.01" />
    </Svg>
  ),
  Folder: (p: IconProps) => (
    <Svg {...p}>
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z" />
    </Svg>
  ),
  File: (p: IconProps) => (
    <Svg {...p}>
      <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8l-5-5Z" />
      <path d="M14 3v5h5" />
    </Svg>
  ),
  Back: (p: IconProps) => (
    <Svg {...p}>
      <path d="M19 12H5M12 19l-7-7 7-7" />
    </Svg>
  ),
  Download: (p: IconProps) => (
    <Svg {...p}>
      <path d="M12 3v12M7 10l5 5 5-5" />
      <path d="M5 21h14" />
    </Svg>
  ),
  Upload: (p: IconProps) => (
    <Svg {...p}>
      <path d="M12 21V9M7 14l5-5 5 5" />
      <path d="M5 3h14" />
    </Svg>
  ),
  FolderPlus: (p: IconProps) => (
    <Svg {...p}>
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z" />
      <path d="M12 11v5M9.5 13.5h5" />
    </Svg>
  ),
  Cpu: (p: IconProps) => (
    <Svg {...p}>
      <rect x="7" y="7" width="10" height="10" rx="1.5" />
      <path d="M9 3v2M15 3v2M9 19v2M15 19v2M3 9h2M3 15h2M19 9h2M19 15h2" />
    </Svg>
  ),
  Check2: (p: IconProps) => (
    <Svg {...p}>
      <path d="M20 6 9 17l-5-5" />
    </Svg>
  ),
  Spinner: ({ size = 16, className }: IconProps) => (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      className={`spin ${className || ""}`}
      aria-hidden="true"
    >
      <path d="M21 12a9 9 0 1 1-6.2-8.6" />
    </svg>
  ),
};
