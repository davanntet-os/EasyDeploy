import type { ReactNode } from "react";
import { Icon } from "./icons";

// Section is a collapsible, card-style form group with a completion indicator
// in its header (a green check once its required fields are satisfied).
export function Section({
  icon: I,
  title,
  subtitle,
  done,
  open,
  onToggle,
  children,
}: {
  icon: (p: { size?: number }) => JSX.Element;
  title: string;
  subtitle: string;
  done?: boolean;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
}) {
  return (
    <div className={`fsection ${open ? "open" : ""}`}>
      <button type="button" className="fsection-head" onClick={onToggle}>
        <span className={`fsection-status ${done ? "done" : ""}`}>{done ? <Icon.Check size={16} /> : <I size={16} />}</span>
        <span className="fsection-titles">
          <span className="fsection-title">{title}</span>
          <span className="fsection-sub">{subtitle}</span>
        </span>
        <span className="fsection-caret">{open ? "▲" : "▾"}</span>
      </button>
      {open && <div className="fsection-body">{children}</div>}
    </div>
  );
}

// Field wraps a control with a label, an optional required marker, and an
// optional help tooltip.
export function Field({
  label,
  required,
  help,
  hint,
  children,
}: {
  label: string;
  required?: boolean;
  help?: string;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <label className="field">
      <span className="field-label">
        {label}
        {help && (
          <span className="field-help" title={help} aria-label={help}>
            ?
          </span>
        )}
        {required && <span className="field-req">*</span>}
        {hint && <span className="field-hint">{hint}</span>}
      </span>
      {children}
    </label>
  );
}

export interface SourceOption {
  key: string;
  icon: (p: { size?: number }) => JSX.Element;
  tag: string;
  title: string;
}

// SourceSelector renders the segmented "what is this service built from" cards.
export function SourceSelector({
  value,
  onChange,
  options,
}: {
  value: string;
  onChange: (key: string) => void;
  options: SourceOption[];
}) {
  return (
    <div className="source-select">
      {options.map((o) => (
        <button
          key={o.key}
          type="button"
          className={`source-card ${value === o.key ? "active" : ""}`}
          onClick={() => onChange(o.key)}
        >
          <span className="source-tag">
            <o.icon size={14} />
            {o.tag}
          </span>
          <span className="source-title">{o.title}</span>
        </button>
      ))}
    </div>
  );
}
