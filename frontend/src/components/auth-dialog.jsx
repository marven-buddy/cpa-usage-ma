import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";

export function AuthDialog({ open, onOpenChange, issue, source, onConnect, onClear }) {
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (open) { setDraft(""); setError(""); }
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={(value) => { if (!busy) onOpenChange(value); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>连接 CPA 管理接口</DialogTitle>
          <DialogDescription>自动复用同源后台的登录凭据，也可在此输入管理密钥，无需在后台勾选“记住密码”。</DialogDescription>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">
          {source === "manual" ? "当前使用插件内输入的管理密钥。" : source === "panel" ? "当前使用后台登录凭据。" : source === "development" ? "当前使用本地开发凭据。" : "当前尚未连接。"}
        </p>
        {(error || issue) && <p role="alert" className="text-sm text-destructive">{error || issue}</p>}
        <form className="space-y-4" onSubmit={async (event) => {
          event.preventDefault();
          setBusy(true); setError("");
          try { await onConnect(draft); setDraft(""); } catch (e) { setError(e.message); } finally { setBusy(false); }
        }}>
          <label className="block space-y-2 text-sm">
            <span>CPA 管理密钥</span>
            <Input type="password" autoComplete="off" aria-label="CPA 管理密钥" placeholder="输入管理密钥" value={draft} disabled={busy} onChange={(event) => setDraft(event.target.value)} />
          </label>
          <p className="text-xs leading-relaxed text-muted-foreground">验证成功后仅保存在当前标签会话，不写入长期存储。清除只移除插件内输入的密钥，不影响后台登录。</p>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" disabled={busy || source !== "manual"} onClick={() => { setDraft(""); setError(""); onClear(); }}>清除手动密钥</Button>
            <Button type="submit" disabled={busy || !draft.trim()}>{busy ? "正在验证…" : "验证并连接"}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
