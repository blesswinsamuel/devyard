import { describe, expect, it } from "vitest";
import { render } from "@solidjs/testing-library";
import type { LogEntry } from "~/data/logs";
import { LogText } from "./log-text";

const entry = (text: string): LogEntry => ({
  id: 1,
  type: "line",
  source: { kind: "service", name: "api" },
  key: "service:api",
  run: 1,
  seq: 1,
  ts: 0,
  stream: "stdout",
  text,
});

describe("LogText", () => {
  it("renders SGR styles, links and search highlights", () => {
    const { container } = render(() => (
      <LogText
        entry={entry("\x1b[31mERROR\x1b[0m see https://example.com/x, error again")}
        highlight={/error/gi}
        current={false}
      />
    ));
    const styled = container.querySelector("span[style]") as HTMLElement;
    expect(styled.style.color).toBe("var(--ansi-red)");
    expect(styled.textContent).toBe("ERROR");
    const link = container.querySelector("a")!;
    expect(link.getAttribute("href")).toBe("https://example.com/x");
    expect(link.getAttribute("rel")).toContain("noopener");
    expect([...container.querySelectorAll("mark")].map((m) => m.textContent)).toEqual(["ERROR", "error"]);
    expect(container.textContent).toBe("ERROR see https://example.com/x, error again");
  });

  it("renders plain text without wrappers", () => {
    const { container } = render(() => <LogText entry={entry("plain line")} highlight={null} current={false} />);
    expect(container.innerHTML).toBe("plain line");
  });
});
