# 派普图片（AC 2.5）接口字段

## 协议身份

- 插件 ID：`paipu-image`。
- Provider ID：`paipu-image`。
- 能力：`image`。
- 默认 Base URL：`https://api.paipu.net`。
- 鉴权驱动：`bearer`。
- 创建：`POST /v1/images/generations`。
- 生命周期：同步响应，上游直接返回 `data[].url` 或 `data[].b64_json`。

## 配置字段

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `apiKey` | secret | 是 | API Key |

## 统一字段映射

| 统一字段 | 类型 | 必填 | 上游映射 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 是 | `model` | 上游图片模型 ID，例如 `lec-ac-image-2-5-flare`。 |
| `prompt` | string | 是 | `prompt` | 图片提示词。 |
| `images` | media[] | 否 | `images` | 参考图，最多 9 张公网 HTTPS 地址；蒙版不会发送。 |
| `imageCount` | integer | 否 | `n` | 输出数量，默认 1。 |
| `aspectRatio` | string | 否 | `aspect_ratio` | 标准比例，例如 `16:9`；留空或 `auto` 时不发送。 |
| `resolution` | string | 否 | `resolution` | 分辨率档位，接受 `1K`/`2K`/`4K`。 |
| `quality` | string | 否 | `resolution` | 质量档位，作为 `resolution` 的兜底来源。 |
| `providerOptions` | object | 否 | `provider-specific fields` | 插件命名空间内的厂商扩展字段。 |

## 上游请求模板逐字段清单

下表由插件请求模板生成，覆盖 body、query、headers 和 multipart 文件声明中的每个字段。

| 上游位置 | 值或转换表达式 |
| --- | --- |
| `create.method` | `"POST"` |
| `create.path` | `"/v1/images/generations"` |
| `create.contentType` | `"application/json"` |

## Provider 扩展键

- `providerOptions.paipu-image.output_format`

动态模型或工作流允许使用文档声明的完整 `parameters/input/extra_body` 对象；该对象是协议本身的开放 schema，不会被宿主裁剪。

## 响应映射逐字段清单

| 映射位置 | 上游路径或转换表达式 |
| --- | --- |
| `response.status` | `"succeeded"` |
| `response.images` | `response.data[].url` 或 `response.data[].b64_json` |
| `response.errorPaths[0]` | `"error.code"` |
| `response.messagePaths[0]` | `"error.message"` |

## 响应与错误

插件把上游同步返回的 `data[].url` 或 `data[].b64_json` 映射为统一结果。HTTP 错误、业务 `error.code` 和 `error.message` 保持失败语义，不包装成成功。

## 兼容边界

- 参考图必须由上游可访问的公网 HTTPS 地址提供，插件按 `requiresPublicMediaUrls` 声明要求宿主下发签名地址，不内联 base64。
- 有参考图时同样固定请求 `POST /v1/images/generations`，不切换到 `/v1/images/edits`。
- 只发送 `aspect_ratio` 与 `resolution`，不发送像素尺寸；`auto` 与空值一律省略。
- 分辨率只接受 `1K`/`2K`/`4K`，其它取值省略，由上游按默认档位处理。

<!-- YINGCE_MANIFEST_CONTRACT_START -->
## Manifest 完整接口定义

以下 JSON 与插件包内实际 `manifest.json` 逐字段一致，覆盖插件身份、权限、配置、鉴权、参数、校验、创建、Agent、查询、取消、结果下载、响应和 Agent 响应映射。`documentation` 字段的值就是当前完整文档；为避免文档在自身内部无限递归，JSON 中仅用等义占位文本表示正文。

```json
{
  "apiVersion": "yingce.plugin/v2",
  "id": "paipu-image",
  "name": "派普图片（AC 2.5）",
  "version": "1.0.0",
  "author": "柚子",
  "description": "派普（paipu）AC 系列图片生成与参考图编辑协议插件，固定走 /v1/images/generations 同步接口。",
  "documentation": "<当前插件的完整 documentation，由 README.md 与 docs/interface.md 拼接而成；为避免 JSON 递归，此处不重复展开正文。>",
  "permissions": [
    "generation.run",
    "media.read"
  ],
  "configuration": {
    "fields": [
      {
        "name": "apiKey",
        "type": "secret",
        "label": "API Key",
        "required": true
      }
    ]
  },
  "contributes": {
    "providers": [
      {
        "id": "paipu-image",
        "label": "派普图片（AC 2.5）",
        "capabilities": [
          "image"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "baseUrl": "https://api.paipu.net",
        "requiresPublicMediaUrls": true,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "上游图片模型 ID，例如 lec-ac-image-2-5-flare。"
          },
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "prompt",
            "description": "图片提示词。"
          },
          {
            "name": "images",
            "type": "media[]",
            "required": false,
            "mapping": "images",
            "description": "参考图，最多 9 张公网 HTTPS 地址；蒙版不会发送。"
          },
          {
            "name": "imageCount",
            "type": "integer",
            "required": false,
            "mapping": "n",
            "description": "输出数量，默认 1。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "required": false,
            "mapping": "aspect_ratio",
            "description": "标准比例，例如 16:9；留空或 auto 时不发送，由上游决定。"
          },
          {
            "name": "resolution",
            "type": "string",
            "required": false,
            "mapping": "resolution",
            "description": "分辨率档位，接受 1K/2K/4K。"
          },
          {
            "name": "quality",
            "type": "string",
            "required": false,
            "mapping": "resolution",
            "description": "质量档位，作为 resolution 的兜底来源。"
          },
          {
            "name": "providerOptions",
            "type": "object",
            "required": false,
            "mapping": "provider-specific fields",
            "description": "插件命名空间内的厂商扩展字段。"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/v1/images/generations",
          "contentType": "application/json",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "prompt": {
              "$ref": "request.prompt"
            },
            "n": {
              "$if": {
                "condition": {
                  "$gt": [
                    {
                      "$ref": "request.imageCount"
                    },
                    0
                  ]
                },
                "then": {
                  "$ref": "request.imageCount"
                },
                "else": 1
              }
            },
            "aspect_ratio": {
              "$omitEmpty": {
                "$if": {
                  "condition": {
                    "$in": [
                      {
                        "$lower": {
                          "$trim": {
                            "$ref": "request.aspectRatio"
                          }
                        }
                      },
                      [
                        "",
                        "auto"
                      ]
                    ]
                  },
                  "then": null,
                  "else": {
                    "$lower": {
                      "$trim": {
                        "$ref": "request.aspectRatio"
                      }
                    }
                  }
                }
              }
            },
            "resolution": {
              "$omitEmpty": {
                "$switch": {
                  "cases": [
                    {
                      "when": {
                        "$in": [
                          {
                            "$lower": {
                              "$trim": {
                                "$coalesce": [
                                  {
                                    "$ref": "request.resolution"
                                  },
                                  {
                                    "$ref": "request.quality"
                                  }
                                ]
                              }
                            }
                          },
                          [
                            "1k",
                            "low",
                            "standard"
                          ]
                        ]
                      },
                      "then": "1K"
                    },
                    {
                      "when": {
                        "$in": [
                          {
                            "$lower": {
                              "$trim": {
                                "$coalesce": [
                                  {
                                    "$ref": "request.resolution"
                                  },
                                  {
                                    "$ref": "request.quality"
                                  }
                                ]
                              }
                            }
                          },
                          [
                            "2k",
                            "medium",
                            "hd"
                          ]
                        ]
                      },
                      "then": "2K"
                    },
                    {
                      "when": {
                        "$in": [
                          {
                            "$lower": {
                              "$trim": {
                                "$coalesce": [
                                  {
                                    "$ref": "request.resolution"
                                  },
                                  {
                                    "$ref": "request.quality"
                                  }
                                ]
                              }
                            }
                          },
                          [
                            "4k",
                            "high",
                            "ultra"
                          ]
                        ]
                      },
                      "then": "4K"
                    }
                  ],
                  "default": null
                }
              }
            },
            "output_format": {
              "$omitEmpty": {
                "$coalesce": [
                  {
                    "$ref": "request.providerOptions.paipu-image.output_format"
                  },
                  "png"
                ]
              }
            },
            "images": {
              "$omitEmpty": {
                "$if": {
                  "condition": {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "request.images"
                        }
                      },
                      0
                    ]
                  },
                  "then": {
                    "$map": {
                      "from": {
                        "$filter": {
                          "from": {
                            "$sortByOrder": {
                              "$ref": "request.images"
                            }
                          },
                          "as": "media",
                          "where": {
                            "$ne": [
                              {
                                "$ref": "media.role"
                              },
                              "mask"
                            ]
                          }
                        }
                      },
                      "as": "media",
                      "in": {
                        "$ref": "media.value"
                      }
                    }
                  },
                  "else": null
                }
              }
            }
          }
        },
        "response": {
          "status": "succeeded",
          "images": {
            "$map": {
              "from": {
                "$ref": "response.data"
              },
              "as": "item",
              "in": {
                "url": {
                  "$omitEmpty": {
                    "$ref": "item.url"
                  }
                },
                "dataUrl": {
                  "$if": {
                    "condition": {
                      "$ref": "item.b64_json"
                    },
                    "then": {
                      "$concat": [
                        "data:image/png;base64,",
                        {
                          "$ref": "item.b64_json"
                        }
                      ]
                    },
                    "else": null
                  }
                }
              }
            }
          },
          "errorPaths": [
            "error.code"
          ],
          "messagePaths": [
            "error.message"
          ]
        }
      }
    ]
  }
}
```
<!-- YINGCE_MANIFEST_CONTRACT_END -->
