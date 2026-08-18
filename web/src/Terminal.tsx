import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { wsURL, type Container } from "./api";
import { Icon } from "./icons";

// Shell is a full interactive terminal into a container. It runs a TTY exec on
// the server and bridges keystrokes/output over a WebSocket: browser input is
// sent as binary stdin, terminal resizes as JSON text control, and container
// output arrives as binary frames written straight into xterm.
export function Shell({ container, onClose, embedded }: { container: Container; onClose?: () => void; embedded?: boolean }) {
  const mountRef = useRef<HTMLDivElement>(null);
  const name = container.Names?.[0]?.replace(/^\//, "") || container.Id.slice(0, 12);

  useEffect(() => {
    if (!mountRef.current) return;
    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
      theme: { background: "#0f1115", foreground: "#e6e9ef", cursor: "#4f8cff" },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(mountRef.current);
    fit.fit();

    const ws = new WebSocket(wsURL(`/containers/${container.Id}/exec`));
    ws.binaryType = "arraybuffer";
    const enc = new TextEncoder();

    const sendResize = () => {
      ws.readyState === WebSocket.OPEN &&
        ws.send(JSON.stringify({ cols: term.cols, rows: term.rows }));
    };

    ws.onopen = () => {
      sendResize();
      term.focus();
    };
    ws.onmessage = (e) => {
      if (e.data instanceof ArrayBuffer) term.write(new Uint8Array(e.data));
      else term.write(String(e.data));
    };
    ws.onclose = () => term.write("\r\n[connection closed]\r\n");

    term.onData((d) => ws.readyState === WebSocket.OPEN && ws.send(enc.encode(d)));

    const onResize = () => {
      try {
        fit.fit();
        sendResize();
      } catch {
        /* terminal not ready */
      }
    };
    window.addEventListener("resize", onResize);

    return () => {
      window.removeEventListener("resize", onResize);
      ws.close();
      term.dispose();
    };
  }, [container.Id]);

  if (embedded) return <div className="term-host" ref={mountRef} />;

  return (
    <div className="modal" onClick={onClose}>
      <div className="modal-body wide term-modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <strong>
            <Icon.Terminal size={15} /> {name} — shell
          </strong>
          <button type="button" onClick={onClose} aria-label="Close">
            <Icon.Close size={16} />
          </button>
        </div>
        <div className="term-host" ref={mountRef} />
      </div>
    </div>
  );
}
