import http from "node:http";
import { randomUUID } from "node:crypto";

const port = Number(process.env.CPA_MOCK_PORT || 18318);
const model = "gpt-4o-mini";
const usage = { prompt_tokens: 100, completion_tokens: 20, total_tokens: 120,
  prompt_tokens_details: { cached_tokens: 40 }, completion_tokens_details: { reasoning_tokens: 5 } };

http.createServer(async (req, res) => {
  const json = (status, value) => {
    res.writeHead(status, { "Content-Type": "application/json" });
    res.end(JSON.stringify(value));
  };
  if (req.url === "/health") return json(200, { service: "cpa-usage-local-upstream" });
  if (req.url === "/v1/models") return json(200, { object: "list", data: [{ id: model, object: "model", owned_by: "local-dev" }] });
  if (req.method !== "POST" || req.url !== "/v1/chat/completions") return json(404, { error: { message: "Not found" } });
  try {
    let raw = "";
    for await (const chunk of req) {
      raw += chunk;
      if (raw.length > 1024 * 1024) return json(413, { error: { message: "Request too large" } });
    }
    const body = JSON.parse(raw);
    if (JSON.stringify(body.messages).includes("[fail]")) return json(400, { error: { message: "Local development failure fixture", type: "invalid_request_error" } });
    const base = { id: `chatcmpl-${randomUUID()}`, created: Math.floor(Date.now() / 1000), model };
    if (body.stream) {
      res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
      for (const frame of [
        { ...base, object: "chat.completion.chunk", choices: [{ index: 0, delta: { role: "assistant", content: "Local development response." }, finish_reason: null }] },
        { ...base, object: "chat.completion.chunk", choices: [{ index: 0, delta: {}, finish_reason: "stop" }] },
        { ...base, object: "chat.completion.chunk", choices: [], usage },
      ]) res.write(`data: ${JSON.stringify(frame)}\n\n`);
      return res.end("data: [DONE]\n\n");
    }
    return json(200, { ...base, object: "chat.completion", choices: [{ index: 0, message: { role: "assistant", content: "Local development response." }, finish_reason: "stop" }], usage });
  } catch (error) {
    return json(400, { error: { message: error.message } });
  }
}).listen(port, "127.0.0.1", () => console.log(`Local upstream: http://127.0.0.1:${port}`));
