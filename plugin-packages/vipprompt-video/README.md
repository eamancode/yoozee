# Vipprompt 视频协议

Vipprompt 系视频模型（渠道模型名形如 `lec-vp-*`）只接受 JSON 请求：`POST /v1/videos` 顶层给 `prompt`，用 `GET /v1/videos/{taskId}` 轮询。与 OpenAI Videos 协议的区别只有一处：不发送 multipart。发 multipart 会被上游拒绝（`Vipprompt video only supports JSON requests`）；把 prompt 包进 `input` 对象同样被拒（`input is not supported for Vipprompt video`）。
