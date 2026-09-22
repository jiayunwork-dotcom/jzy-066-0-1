# 一维 Stefan 凝固锋面核算服务

半无限大物料从一侧被持续冷却（壁温恒定 `Tw < Tf`）时，凝固壳厚度按
Neumann 相似解推进。本服务把这套核算做成常驻 HTTP 服务：热工分析工具提交
壁温、凝固点与材料物性，拿回 **Stefan 数、相似常数 λ 及其求根残差、
锋面位置、壁面热流**，也可凭目标厚度反求时间、提交可取消的长时间序列作业，
并能把工况按名字持久化保存。

- 语言/框架：Go 1.22 + Gin，仅 HTTP/JSON，无网页界面
- 持久化：容器内 `/data` 目录下的 JSON 工况档（可挂卷）
- 预置算例：`ice-wall-minus30c`（冰，壁温 −30 ℃，一小时凝固约 3.8 cm）

## 物理模型

```
Ste = c (Tf - Tw) / Lf
√π · λ · e^(λ²) · erf(λ) = Ste        ← λ 的超越方程，数值二分求根
s(t) = 2 λ √(α t)                     ← 锋面位置（m）
q"(t) = k (Tf - Tw) / ( √(π α t) · erf(λ) )   ← 壁面热流（W/m²，指向物料为正）
t    = ( s / (2λ) )² / α              ← 由目标厚度反求时间
```

- 误差函数 `erf` 由本仓库 `internal/erf` 自行实现（幂级数 + erfc 连分式，
  Lentz 算法），并有与标准库逐项比对、erf/erfc 不可混用的守卫测试。
- 对外的 λ 永远是精确求根结果；小 Stefan 数近似 `λ₀ = √(Ste/2)` 仅以
  `approx_*` 字段作为**明确标注的对照分支**返回。
- `t=0` 时锋面为零，壁面瞬时热流按 `1/√t` 发散，该字段输出 `null`。
- 物性校验在解方程之前完成：`α≤0`、`Lf≤0`、`c≤0`、`k≤0`、`Tw≥Tf`
  一律返回 `422` 与稳定错误码、中文原因。

## 包结构（求根内核与推进计算分离）

| 包 | 职责 |
| --- | --- |
| `internal/erf` | 误差函数/余误差函数实现 |
| `internal/stefan` | Stefan 数、超越方程求根、精确/近似分支 |
| `internal/front` | 锋面位置、壁面热流、时间反求、物性校验 |
| `internal/profile` | 命名工况档持久化（JSON + 原子写） |
| `internal/job` | 可取消的锋面时间序列作业 |
| `internal/httpx` | Gin 路由、DTO、统一错误结构 |
| `cmd/server` | 服务入口（预置冰层算例、信号关停） |

## 运行

```bash
docker build -t stefan-front .          # 构建时自动执行 go test ./...
docker run --rm -p 8080:8080 stefan-front
```

本地直接运行：`go test ./... && go run ./cmd/server`
（环境变量 `STEFAN_ADDR=:8080`、`STEFAN_DATA_DIR=./data`）。

## HTTP 接口（前缀 `/api/v1`）

### 正向核算 `POST /forward`

```json
{
  "c": 2100, "k": 2.22, "alpha": 1.15e-6, "lf": 334000,
  "tf": 273.15, "tw": 243.15, "time_s": 3600
}
```

也可用 `"profile": "ice-wall-minus30c"` 引用工况档，请求体中显式给出的
字段逐字段覆盖工况档。返回（数值随冰物性示例）：

```json
{
  "ste": 0.1886, "time_s": 3600, "front_m": 0.03836,
  "wall_heat_flux_w_m2": 1787.8,
  "heat_extraction_w_m2": 1787.8,
  "cumulative_heat_j_m2": 1.2872e7,
  "root": {
    "lambda": 0.29809, "residual": -1.08e-12, "residual_over_ste": -5.74e-12,
    "iterations": 37, "ste": 0.1886,
    "approx_lambda": 0.30710, "approx_residual": 0.01232,
    "approx_note": "精确分支：...approx_lambda=sqrt(Ste/2) 仅为小 Ste 对照..."
  },
  "approx_front_m": 0.03952,
  "approx_quality": "out_of_range: 已超出小 Ste 适用区间，近似不可用于定量结论"
}
```

### 反向核算 `POST /inverse`

请求体同上去掉 `time_s`，给 `"target_front_m": 0.05`；返回 `time_s`、
人类可读时长 `time_human` 以及正算复核 `verified_front_m`。

### 工况档

- `GET /profiles`：列出全部（含内置冰层档）
- `POST /profiles`：`{"name": "...", "c": ..., "k": ..., "alpha": ..., "lf": ..., "tf": ..., "tw": ...}`
- `GET|PUT|DELETE /profiles/:name`

### 可取消时间序列作业

- `POST /series`：`{ ...物性..., "times_s": [0, 60, 600, 3600] }` →
  `202` 与 `id`
- `GET /series/:id`：`status ∈ queued|running|completed|canceled|failed`、
  `progress/total`；**仅 `completed` 才带 `points`**，取消的作业绝不返回半列点
- `POST /series/:id/cancel`：协作式取消

### 健康检查 `GET /healthz`

错误响应统一为 `{"code": "...", "message": "..."}`，例如
`{"code":"wall_not_below_freeze","message":"front: 壁温 Tw 必须严格低于凝固点 Tf，否则不会凝固"}`。

## 测试覆盖

`go test ./...`（镜像构建时强制执行）覆盖：

- t=0 锋面恒为零、热流为 `null`；
- `t→4t` 锋面正好加倍（√t 关系）、热流按 `1/√t` 衰减；
- 加大温差锋面更深；潜热趋于很大（Ste→0）锋面停滞，并精确核对小 Ste 下
  的 `1/√Lf` 标度；
- 超越方程精确根残差收敛；小 Ste 下近似 `√(Ste/2)` 相对误差 <1%，
  中等 Ste（如 Ste=1）下近似明显偏离、残差超标；
- **erf 误写成 erfc 的守卫测试**（错误方程有界、无根，必然暴露）；
- 非法物性（α、Lf 非正，Tw≥Tf，非有限数）在求根前被拦截；
- 作业取消不泄漏部分点列、多工况并行时 λ 与锋面互不渗透；
- HTTP 全链路：正/反向、工况档 CRUD、内置冰档厘米级结果、作业轮询与取消。
