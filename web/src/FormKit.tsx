import { useState, type ReactNode } from "react";
import { Icon } from "./icons";

// SearchPicker is an input with a searchable dropdown of suggestions. It still
// allows typing a value that isn't in the list (e.g. a not-yet-created volume /
// network). Used for volume mount sources and the service network field.
export function SearchPicker({
  value,
  options,
  onChange,
  placeholder,
  icon: I = Icon.Search,
}: {
  value: string;
  options: string[];
  onChange: (v: string) => void;
  placeholder?: string;
  icon?: (p: { size?: number }) => JSX.Element;
}) {
  const [open, setOpen] = useState(false);
  const q = value.trim().toLowerCase();
  const matches = options.filter((o) => o.toLowerCase().includes(q));
  return (
    <div className="vol-picker">
      <input
        placeholder={placeholder}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => setTimeout(() => setOpen(false), 120)}
      />
      {open && matches.length > 0 && (
        <ul className="vol-picker-list">
          {matches.slice(0, 8).map((o) => (
            <li key={o}>
              <button
                type="button"
                onMouseDown={(e) => {
                  e.preventDefault();
                  onChange(o);
                  setOpen(false);
                }}
              >
                <I size={13} /> <span>{o}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

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
