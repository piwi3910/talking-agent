// Small shared building blocks for the Stage console screens.
import React from "react";
import type { Live, Route } from "./main";
import { Agent, accentOf, markOf } from "./api";
import { Back, Logo, Moon, Sun } from "./icons";

export function Brand({ subtitle }: { subtitle?: string }) {
  return (
    <div className="brand">
      <span className="brand-tile" aria-hidden="true">
        <Logo />
      </span>
      <span className="brand-text">
        <strong>STAGE</strong>
        <small>{subtitle || "Talking Agent Demo"}</small>
      </span>
    </div>
  );
}

export function ThemeToggle({ live }: { live: Live }) {
  const dark = live.theme === "dark";
  return (
    <button
      type="button"
      className="icon-button"
      aria-label={dark ? "Switch to light theme" : "Switch to dark theme"}
      title={dark ? "Light theme" : "Dark theme"}
      onClick={() => live.setTheme(dark ? "light" : "dark")}
    >
      {dark ? <Sun /> : <Moon />}
    </button>
  );
}

export function BackButton({
  live,
  to,
  label,
}: {
  live: Live;
  to: Route;
  label: string;
}) {
  return (
    <a
      className="icon-button"
      href={to.screen === "launcher" ? "/" : `/${to.screen}`}
      aria-label={label}
      onClick={(e) => {
        e.preventDefault();
        live.navigate(to);
      }}
    >
      <Back />
    </a>
  );
}

// An internal link that keeps the live session (client-side navigation).
export function NavLink({
  live,
  to,
  className,
  children,
  ...rest
}: {
  live: Live;
  to: Route;
  className?: string;
  children: React.ReactNode;
} & Omit<React.AnchorHTMLAttributes<HTMLAnchorElement>, "href">) {
  const href =
    to.screen === "settings"
      ? to.section === "channels"
        ? "/settings"
        : `/settings/${to.section}`
      : to.screen === "launcher"
        ? "/"
        : `/${to.screen}`;
  return (
    <a
      {...rest}
      className={className}
      href={href}
      onClick={(e) => {
        if (e.metaKey || e.ctrlKey || e.shiftKey) return;
        e.preventDefault();
        live.navigate(to);
      }}
    >
      {children}
    </a>
  );
}

export function PersonaMark({
  agent,
  size = "md",
}: {
  agent?: Agent;
  size?: "sm" | "md" | "lg";
}) {
  const accent = accentOf(agent);
  return (
    <span
      className={`persona-mark ${size}`}
      aria-hidden="true"
      style={
        {
          "--accent-dark": accent.dark,
          "--accent-light": accent.light,
        } as React.CSSProperties
      }
    >
      {markOf(agent)}
    </span>
  );
}

export function Switch({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (value: boolean) => void;
}) {
  return (
    <div className="switch-row">
      <span>
        <span className="switch-label">{label}</span>
        {hint && <small>{hint}</small>}
      </span>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        aria-label={label}
        className="switch"
        onClick={() => onChange(!checked)}
      >
        <span />
      </button>
    </div>
  );
}

export function NewBadge() {
  return <span className="badge-new">NEW</span>;
}
