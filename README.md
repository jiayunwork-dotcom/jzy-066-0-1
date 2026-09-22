# stefand — 一维 Stefan 凝固锋面核算服务

材料从一侧被持续冷却时，凝固界面按 √t 规律向内部推进。本服务只做这一
一维 Neumann/Stefan 凝固问题的**解析核算**，通过 HTTP 对外提供：
相似常数 λ、锋面位置 s(t)、壁面热流，以及时间反求、工况档持久化和
可取消的长时间序列作业。无网页界面，与冷库巡检、设备台账等业务无关。

## 物理模型

固相温度分布（Neumann 解）

```
T(x,t) = Tw + (Tf − Tw) · erf(x/(2√(αt))) / erf(λ)
```

- Stefan 数：`Ste = c·(Tf − Tw)/Lf`
- 相似常数 λ 满足超越方程（**erf，不是 erfc**）：

  ```
  √π · λ · exp(λ²) · erf(λ) = Ste
  ```

  无初等解，服务内用倍增找括号 + 二分法数值求根，残差随结果回报。
- 锋面位置：`s(t) = 2λ√(αt)`
- 锋面速度：`ds/dt = λ√(α/t)`
- 壁面热流（由壁面温度梯度导出，t>0）：

  ```
  q_w(t) = k(Tf−Tw)/(√(παt)) · e^{−λ²}/erf(λ)
  ```

  理想阶跃冷边界下 t=0 的热流为 +∞，接口在该点输出 `null` 并附说明。
- 时间反求：`t = (s/(2λ))²/α`

响应里同时带 `lambda_approx = √(Ste/2)` 这一**小 Stefan 数近似对照
分支**及其代回残差，并明确标注它不能冒充精确根；中等 Ste 下该近似残差
显著超标（测试钉死两侧）。

## 包结构（内核与推进分离）

| 包 | 职责 |
| --- | --- |
| `internal/erfx` | 自行实现的 erf/erfc（幂级数 + Laplace 连分式），互不混淆 |
| `internal/stefan` | 参数校验、Stefan 数、超越方程数值求根、近似对照分支 |
| `internal/front` | 锋面推进、壁面热流、可取消时间序列 |
| `internal/reverse` | 目标厚度 → 所需时间 |
| `internal/profile` | 命名工况档，JSON 文件原子写持久化，预置冰层算例 |
| `internal/jobs` | 异步可取消作业生命周期，取消绝不返回半成品点列 |
| `internal/server` | Gin HTTP 路由与协议适配 |
| `cmd/stefand` | 服务入口；`cmd/healthprobe` 为容器健康探针 |

全部计算均为无共享可变状态的纯函数调用，多组工况并行时各自的
λ 与锋面互不渗透（有并发隔离测试与 `-race` 覆盖）。

## 构建与运行

```bash
docker build -t stefand .          # 构建时自动 go test ./...
docker run -p 8080:8080 -v stefan-data:/data stefand
```

容器启动即在 8080 提供接口，并在 `/data/profiles.json` 持久化工况档。
环境变量：`STEFAN_PORT`（默认 8080）、`STEFAN_DATA_DIR`（默认 /data）。

本地：

```bash
go test ./...
go run ./cmd/stefand
```

## HTTP 接口（前缀 `/api/v1`）

物性字段：`c`(比热)、`lf`(潜热)、`k`(导热系数)、`alpha`(热扩散率)、
`tf`(凝固点)、`tw`(壁温)。每次请求可内联 `params`，也可用 `profile`
引用已建档名字（二选一）。

### 正向核算 `POST /stefan/forward`

```json
{ "profile": "ice-wall-minus20", "time": 3600 }
```

返回 `ste`、`lambda`（精确根，含 `abs_residual`/`rel_residual`/迭代次数）、
`lambda_approx`（对照分支+警示说明）、`front`、`front_speed`、
`wall_heat_flux`。

### 时间反求 `POST /stefan/reverse`

```json
{ "profile": "ice-wall-minus20", "s": 0.05 }
```

返回达到 5 cm 所需的 `time`（秒）及该瞬间锋面速度。

### 工况档

- `GET /profiles` 列出全部档
- `POST /profiles` 建档（重名 409）
- `GET /profiles/:name` / `DELETE /profiles/:name`

服务预置 `ice-wall-minus20`（纯水冰，壁温 −20 ℃）：一小时凝固厚度约
**3.16 cm**，拉起即可核对。

### 可取消作业（长序列）

- `POST /jobs`：`{"profile":..., "t0":0, "t1":86400, "intervals":1000}`，
  返回 202 与作业 id。
- `GET /jobs/:id`：查询状态（queued/running/completed/canceled/failed）；
  仅 completed 携带完整 `points`。
- `POST /jobs/:id/cancel`：取消；canceled 状态**不带任何点列**。

错误统一为 `{ "code", "message", "field?" }`，非法物性在解方程之前拦截：

| 情形 | HTTP | code |
| --- | --- | --- |
| α 或 Lf 等非正、Tw ≥ Tf | 400 | `invalid_parameter`（带 field 与中文原因） |
| 时间为负 / 厚度非正 | 400 | `invalid_time` / `invalid_thickness` |
| 档/作业不存在 | 404 | `profile_not_found` / `job_not_found` |

## 测试钉死的关系

- t=0 时锋面位置恒为零；
- `s(4t) = 2·s(t)`（√t 律）；
- 只增大温差，同一时刻锋面更深、热流更大；
- Lf→∞（Ste→0）锋面趋于停滞；
- 小 Ste 区间精确根与 √(Ste/2) 相对误差 < 2%，Ste=1 时近似相对误差
  >10%、代回相对残差 >20%（精确分支仍机器精度收敛）；
- 专项哨兵测试：把方程里的 erf 换成 erfc 会立刻失败；近似越界使用同样被抓；
- 非法物性（α≤0、Lf≤0、Tw≥Tf）拦截并回带原因；
- 大作业取消后只返回 `canceled` 与说明，绝不泄露半成品点列；
- 32 goroutine × 多工况并行结果与单线程基准逐位一致。
