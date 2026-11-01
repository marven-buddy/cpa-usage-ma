import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";

export function KeyNoteDialog({ row, onClose, onSave }) {
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => { setDraft(row?.key_note || ""); setError(""); }, [row]);
  return (
    <Dialog open={Boolean(row)} onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑 Key 备注</DialogTitle>
          <DialogDescription>{row?.key_label || row?.key} · 备注只修改显示名称，不改变统计归属。</DialogDescription>
        </DialogHeader>
        <form className="space-y-4" onSubmit={async (event) => {
          event.preventDefault(); setBusy(true); setError("");
          try { await onSave(row.key, draft.trim()); onClose(); } catch (e) { setError(e.message); } finally { setBusy(false); }
        }}>
          <label className="block space-y-2 text-sm">
            <span>备注</span>
            <Input aria-label="Key 备注" placeholder="例如：办公电脑、自动化任务" maxLength={80} value={draft} disabled={busy} onChange={(event) => setDraft(event.target.value)} />
          </label>
          <p className="text-xs text-muted-foreground">最多 80 个字符，留空保存可清除备注。备注保存在本机数据库，刷新或更换浏览器后仍可使用。</p>
          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" disabled={busy} onClick={onClose}>取消</Button>
            <Button type="submit" disabled={busy}>{busy ? "保存中…" : "保存备注"}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
