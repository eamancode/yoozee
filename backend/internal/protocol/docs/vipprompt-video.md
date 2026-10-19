# Vipprompt 视频协议

Vipprompt 系视频模型（渠道模型名形如 `lec-vp-*`）只接受 **JSON** 请求：在 `POST /v1/videos` 的请求体顶层直接给 `prompt`，用 `GET /v1/videos/{task_id}` 轮询，完成后从查询响应里取视频地址。本协议与 OpenAI Videos 适配器的字段完全一致，区别只有一处：不发送 `multipart/form-data`。发 multipart 会被上游直接拒绝，报 `Vipprompt video only supports JSON requests`；把 prompt 包进 `input` 对象同样会被拒绝，报 `input is not supported for Vipprompt video`。

## 接口与鉴权

{{OPERATIONS}}

```http
POST {channel_base_url}/v1/videos
GET  {channel_base_url}/v1/videos/{task_id}
Authorization: Bearer ***
Content-Type: application/json
```

创建响应的 `id` 即任务 ID，查询时对任务 ID 做 URL 编码后拼到路径里。任务进入 `completed` 或 `failed` 即结束；成功时使用查询响应中的完整 `url`，该地址是已签名链接，取文件时不需要再带 API Key。

## 模型、计费与素材上限

模型清单、计费、时长范围、分辨率档位与素材数量都由当前 Vipprompt 部署决定，不能在协议层写死。管理员应在渠道的模型配置里登记经过实际调用验证的模型 ID，并把真实支持的时长范围与分辨率写进该模型的能力配置，避免下游按错误上限提交。

本协议按宿主传入的 `duration` 发送 `seconds`，按 `aspectRatio` 发送 `size`。若某个模型的 `size` 需要像素写法（例如 `1280x720`）而不是比例写法，应在模型能力配置里约束可选比例，或为该模型单独补充适配，不要在上游拒绝后反复重试同一请求。

## 参数与字段映射

{{PARAMETERS}}

实际映射：`model` 与 `prompt` 直接放在请求体顶层；`duration` 映射为字符串 `seconds`；`aspectRatio` 映射为 `size`；参考图映射为 `input_reference` 数组。当前适配器不发送参考视频、参考音频、`resolution`、`generateAudio` 与 `watermark`，需要这些能力时应先确认上游支持，再扩展适配器并补充测试。

## 创建示例

```bash
curl "{channel_base_url}/v1/videos" \
  -H "Authorization: Bearer ***" \
  -H "Content-Type: application/json" \
  -d '{
    "model":"lec-vp-seedance-2-5-m2",
    "prompt":"凌晨三点的空荡医院走廊，护士推着治疗车经过紧闭的病房门，冷白日光灯轻微频闪",
    "seconds":"5",
    "size":"9:16"
  }'
```

## 响应、轮询与排错

创建成功后用返回的 `id` 轮询 `GET /v1/videos/{task_id}`，直到 `status` 进入 `completed` 或 `failed`。完成时解析查询响应里的 `url`；若上游把地址放在更深层的字段里，必须为该协议补专属解析，不能把空结果当成功。

常见失败：用 multipart 提交（被拒为 only supports JSON）、prompt 包在 `input` 里（被拒为 input is not supported）、`size` 用了上游不认的写法、时长超出模型范围、模型 ID 未在渠道映射。先看上游返回的原文再改配置，不要靠加别名掩盖协议差异。

## 官方资料与边界

- 接口形状以该中转站文档 `POST /v1/videos` → `GET /v1/videos/{task_id}` → `GET /v1/videos/{task_id}/content` 为准。
- 上游模型厂商文档只用于确认模型能力，不能替代网关的字段合同。

{{CONTRACT}}
