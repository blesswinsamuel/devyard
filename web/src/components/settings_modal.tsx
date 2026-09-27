import { Show, createEffect, createSignal } from "solid-js";
import { Globe, Info, Network } from "lucide-solid";
import { fetchGlobalConfig, globalConfig, updateGlobalConfig } from "~/stores/data";
import { setShowSettingsModal, showSettingsModal } from "~/stores/app";
import { Button } from "~/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Separator } from "~/components/ui/separator";

export function SettingsModal() {
  const [webHost, setWebHost] = createSignal("");
  const [webPort, setWebPort] = createSignal("");
  const [proxyHost, setProxyHost] = createSignal("");
  const [proxyPort, setProxyPort] = createSignal("");
  const [proxyDomainSuffix, setProxyDomainSuffix] = createSignal("");
  const [proxyTLSEnabled, setProxyTLSEnabled] = createSignal(false);
  const [proxyTLSPort, setProxyTLSPort] = createSignal("8443");
  const [proxyTLSRedirect, setProxyTLSRedirect] = createSignal(false);
  const [proxyTLSCertFile, setProxyTLSCertFile] = createSignal("");
  const [proxyTLSKeyFile, setProxyTLSKeyFile] = createSignal("");
  const [saving, setSaving] = createSignal(false);

  createEffect(() => {
    if (showSettingsModal()) {
      fetchGlobalConfig();
    }
  });

  createEffect(() => {
    const cfg = globalConfig();
    if (cfg && showSettingsModal()) {
      setWebHost(cfg.web?.host ?? "");
      setWebPort(String(cfg.web?.port ?? ""));
      setProxyHost(cfg.proxy?.host ?? "");
      setProxyPort(String(cfg.proxy?.port ?? ""));
      setProxyDomainSuffix(cfg.proxy?.domainSuffix ?? (cfg.proxy as any)?.domain_suffix ?? "");
      setProxyTLSEnabled(Boolean(cfg.proxy?.tls?.enabled));
      setProxyTLSPort(String(cfg.proxy?.tls?.port || 8443));
      setProxyTLSRedirect(Boolean(cfg.proxy?.tls?.httpRedirect ?? (cfg.proxy?.tls as any)?.http_redirect));
      setProxyTLSCertFile(cfg.proxy?.tls?.certFile ?? (cfg.proxy?.tls as any)?.cert_file ?? "");
      setProxyTLSKeyFile(cfg.proxy?.tls?.keyFile ?? (cfg.proxy?.tls as any)?.key_file ?? "");
    }
  });

  const handleSave = async (e: SubmitEvent) => {
    e.preventDefault();
    const cfg = globalConfig();
    if (!cfg || saving()) return;
    setSaving(true);
    const ok = await updateGlobalConfig({
      ...cfg,
      web: { ...cfg.web, host: webHost().trim(), port: Number(webPort()) },
      proxy: {
        ...cfg.proxy,
        host: proxyHost().trim(),
        port: Number(proxyPort()),
        domainSuffix: proxyDomainSuffix().trim(),
        tls: {
          enabled: proxyTLSEnabled(),
          port: Number(proxyTLSPort()) || 8443,
          httpRedirect: proxyTLSRedirect(),
          certFile: proxyTLSCertFile().trim(),
          keyFile: proxyTLSKeyFile().trim(),
        },
      },
    });
    setSaving(false);
    if (ok) {
      setShowSettingsModal(false);
    }
  };

  return (
    <Dialog open={showSettingsModal()} onOpenChange={setShowSettingsModal}>
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Settings</DialogTitle>
          <DialogDescription class="sr-only">Edit the global devyard config.</DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSave} class="space-y-4 text-[13px]">
          <div>
            <div class="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <Globe class="size-3.5" />
              Web UI defaults <span class="opacity-60">(devyard web)</span>
            </div>
            <div class="grid grid-cols-[1fr_90px] gap-2">
              <div class="space-y-1.5">
                <Label for="settings-web-host" class="text-xs text-muted-foreground">
                  Host
                </Label>
                <Input
                  id="settings-web-host"
                  type="text"
                  required
                  value={webHost()}
                  onInput={(e) => setWebHost(e.currentTarget.value)}
                  placeholder="127.0.0.1"
                  class="rounded-md px-3 font-mono text-xs md:text-xs"
                />
              </div>
              <div class="space-y-1.5">
                <Label for="settings-web-port" class="text-xs text-muted-foreground">
                  Port
                </Label>
                <Input
                  id="settings-web-port"
                  type="number"
                  min="1"
                  max="65535"
                  required
                  value={webPort()}
                  onInput={(e) => setWebPort(e.currentTarget.value)}
                  class="rounded-md px-3 font-mono text-xs md:text-xs"
                />
              </div>
            </div>
          </div>

          <Separator class="opacity-60" />

          <div>
            <div class="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <Network class="size-3.5" />
              Reverse proxy
            </div>
            <div class="space-y-3">
              <div class="grid grid-cols-[1fr_90px] gap-2">
                <div class="space-y-1.5">
                  <Label for="settings-proxy-host" class="text-xs text-muted-foreground">
                    Host
                  </Label>
                  <Input
                    id="settings-proxy-host"
                    type="text"
                    required
                    value={proxyHost()}
                    onInput={(e) => setProxyHost(e.currentTarget.value)}
                    placeholder="127.0.0.1"
                    class="rounded-md px-3 font-mono text-xs md:text-xs"
                  />
                </div>
                <div class="space-y-1.5">
                  <Label for="settings-proxy-port" class="text-xs text-muted-foreground">
                    Port
                  </Label>
                  <Input
                    id="settings-proxy-port"
                    type="number"
                    min="1"
                    max="65535"
                    required
                    value={proxyPort()}
                    onInput={(e) => setProxyPort(e.currentTarget.value)}
                    class="rounded-md px-3 font-mono text-xs md:text-xs"
                  />
                </div>
              </div>
              <div class="space-y-1.5">
                <Label for="settings-proxy-suffix" class="text-xs text-muted-foreground">
                  Domain suffix
                </Label>
                <Input
                  id="settings-proxy-suffix"
                  type="text"
                  required
                  value={proxyDomainSuffix()}
                  onInput={(e) => setProxyDomainSuffix(e.currentTarget.value)}
                  placeholder="localhost"
                  class="rounded-md px-3 font-mono text-xs md:text-xs"
                />
              </div>

              <div class="pt-2 border-t border-border/40 space-y-3">
                <div class="flex items-center justify-between">
                  <div class="space-y-0.5">
                    <Label for="settings-proxy-tls" class="text-xs font-medium cursor-pointer">
                      Enable TLS (HTTPS)
                    </Label>
                    <div class="text-[11px] text-muted-foreground">
                      Serves routes over HTTPS with automatic or custom certificates
                    </div>
                  </div>
                  <input
                    id="settings-proxy-tls"
                    type="checkbox"
                    checked={proxyTLSEnabled()}
                    onChange={(e) => setProxyTLSEnabled(e.currentTarget.checked)}
                    class="h-4 w-4 rounded border-border text-primary cursor-pointer accent-primary"
                  />
                </div>

                <Show when={proxyTLSEnabled()}>
                  <div class="space-y-3 pl-2 border-l-2 border-primary/20">
                    <div class="grid grid-cols-[1fr_90px] gap-2 items-center">
                      <div class="flex items-center gap-2">
                        <input
                          id="settings-proxy-redirect"
                          type="checkbox"
                          checked={proxyTLSRedirect()}
                          onChange={(e) => setProxyTLSRedirect(e.currentTarget.checked)}
                          class="h-3.5 w-3.5 rounded border-border text-primary cursor-pointer accent-primary"
                        />
                        <Label for="settings-proxy-redirect" class="text-xs text-muted-foreground cursor-pointer">
                          Redirect HTTP to HTTPS
                        </Label>
                      </div>
                      <div class="space-y-1">
                        <Label for="settings-proxy-tls-port" class="text-xs text-muted-foreground">
                          TLS Port
                        </Label>
                        <Input
                          id="settings-proxy-tls-port"
                          type="number"
                          min="1"
                          max="65535"
                          value={proxyTLSPort()}
                          onInput={(e) => setProxyTLSPort(e.currentTarget.value)}
                          class="rounded-md px-3 font-mono text-xs md:text-xs"
                        />
                      </div>
                    </div>
                    <div class="space-y-1">
                      <Label for="settings-proxy-cert-file" class="text-xs text-muted-foreground">
                        Custom Cert File (optional)
                      </Label>
                      <Input
                        id="settings-proxy-cert-file"
                        type="text"
                        placeholder="Leave empty for auto-generated CA / mkcert"
                        value={proxyTLSCertFile()}
                        onInput={(e) => setProxyTLSCertFile(e.currentTarget.value)}
                        class="rounded-md px-3 font-mono text-xs md:text-xs"
                      />
                    </div>
                    <div class="space-y-1">
                      <Label for="settings-proxy-key-file" class="text-xs text-muted-foreground">
                        Custom Key File (optional)
                      </Label>
                      <Input
                        id="settings-proxy-key-file"
                        type="text"
                        placeholder="Leave empty for auto-generated CA / mkcert"
                        value={proxyTLSKeyFile()}
                        onInput={(e) => setProxyTLSKeyFile(e.currentTarget.value)}
                        class="rounded-md px-3 font-mono text-xs md:text-xs"
                      />
                    </div>
                  </div>
                </Show>
              </div>
            </div>
          </div>

          <div class="flex items-start gap-1.5 rounded-md bg-muted/60 px-3 py-2 text-xs text-muted-foreground">
            <Info class="mt-0.5 size-3.5 shrink-0" />
            <span>
              Saved to <code class="font-mono">~/.config/devyard/config.yml</code>. The reverse proxy binds at daemon
              startup, so restart the daemon for proxy changes to take effect.
            </span>
          </div>

          <div class="flex items-center justify-between gap-2 pt-1">
            <Show when={globalConfig()}>
              {(cfg) => (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setWebHost(cfg().web?.host ?? "");
                    setWebPort(String(cfg().web?.port ?? ""));
                    setProxyHost(cfg().proxy?.host ?? "");
                    setProxyPort(String(cfg().proxy?.port ?? ""));
                    setProxyDomainSuffix(cfg().proxy?.domainSuffix ?? (cfg().proxy as any)?.domain_suffix ?? "");
                    setProxyTLSEnabled(Boolean(cfg().proxy?.tls?.enabled));
                    setProxyTLSPort(String(cfg().proxy?.tls?.port || 8443));
                    setProxyTLSRedirect(Boolean(cfg().proxy?.tls?.httpRedirect ?? (cfg().proxy?.tls as any)?.http_redirect));
                    setProxyTLSCertFile(cfg().proxy?.tls?.certFile ?? (cfg().proxy?.tls as any)?.cert_file ?? "");
                    setProxyTLSKeyFile(cfg().proxy?.tls?.keyFile ?? (cfg().proxy?.tls as any)?.key_file ?? "");
                  }}
                >
                  Reset
                </Button>
              )}
            </Show>
            <div class="ml-auto flex gap-2">
              <Button type="button" variant="outline" size="sm" onClick={() => setShowSettingsModal(false)}>
                Cancel
              </Button>
              <Button type="submit" size="sm" disabled={saving()}>
                {saving() ? "Saving…" : "Save"}
              </Button>
            </div>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
