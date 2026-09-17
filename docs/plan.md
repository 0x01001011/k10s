# Execution plan

Backlog k10s ở dạng **task card cho worker agent**. Mỗi card tự đủ context:
một agent đọc card + section [Worker contract](#worker-contract) là đủ để làm,
không cần hỏi lại.

Dispatcher: chọn card → dán [prompt template](#prompt-template) với `<ID>` →
review diff → tick `[x]` ở [Board](#board).

---

## Worker contract

Đọc phần này **trước** khi sửa bất kỳ file nào. Đây là các invariant của repo,
phá là CI đỏ hoặc UI vỡ âm thầm.

### Kiến trúc

- `internal/domain` là boundary duy nhất giữa UI và backend.
  `internal/ui` **không được** import `internal/k8s` hay `internal/mock`.
- **Không thêm method mới vào `domain.Source`.** Interface đã 25 method và có
  4 impl (`k8s.Store`, `mock.Source`, các stub trong `internal/ui/*_test.go`,
  `connect_test.go:pendingSource`). Capability mới đi bằng **optional
  interface**:

  ```go
  // internal/domain/domain.go
  //
  // Containers lists the containers of a pod-bearing object. Backends that
  // cannot answer simply do not implement it; the UI then hides the picker.
  type Containers interface {
      Containers(kind, ns, name string) ([]string, error)
  }
  ```

  UI dùng type-assert, degrade im lặng khi thiếu:

  ```go
  if c, ok := m.src.(domain.Containers); ok { … }
  ```

  Cả `internal/k8s` **và** `internal/mock` phải impl (nếu không thì demo backend
  và `just shot` không thấy feature → không review được UI).

### Performance (có test gác, hỏng = regression thật)

- Render path **không được I/O**. Guard: `TestViewDoesNotBuildRows`,
  `TestKeypressLatency` trong `internal/ui`.
- `RowCount` **không format row, không mở watch**. Guard:
  `TestRowCount`, `TestNewStoreReturnsFast`.
- Informer là lazy per-kind. Startup đăng ký **zero** watch. Không thêm gì vào
  đường khởi động làm nó gọi API server.
- Đọc `docs/performance.md` trước khi chạm `internal/k8s/store.go` hoặc
  `counts.go`.

### Render

- **Mọi dòng render ra phải đúng bằng width terminal.** Overlay/join dựa vào đó.
  Guard: `internal/ui/block_test.go`.
- Không nest style trong `Render` của style khác (reset bên trong nuốt
  background bên ngoài) → dùng `padBG` + sibling run.
- Verify UI không cần TTY: `just shot 140 44 "j,j,d"`.

### Config

`internal/config/config.go` dùng **parser YAML tự viết**, phẳng, chỉ 1 cấp
nesting, **không hỗ trợ list-of-map**. Thêm field = sửa **cả** `render()` **và**
`parse()` **và** thêm case test. Cần cấu trúc phức tạp hơn (ví dụ saved views)
→ file riêng trong `~/.k10s/`, đừng nhồi vào `config.yaml`.

### Thêm resource kind

Theo đúng 5 bước ở `docs/dev.md` § "Adding a resource kind". Bỏ bước 5
(mirror vào `internal/mock/data.go`) là demo backend lệch với live.

### Definition of done

Card chỉ xong khi **tất cả** đúng:

1. `just check` xanh (= `fmt-check` + `vet` + `test`).
2. Có test mới cover đúng hành vi card mô tả. Backend test dùng fake clientset
   (`newTestStore`), nhớ `syncKinds(t, s, kPods, …)` trước khi assert rows.
3. Feature nhìn thấy được trên demo backend → dán output `just shot` vào PR.
4. Docs cập nhật: `docs/keybindings.md` nếu thêm phím, `docs/commands.md` nếu
   thêm command, `docs/config.md` nếu thêm config key, README bảng feature nếu
   user thấy được.
5. Tick card này trong `docs/plan.md` § Board.
6. Không đụng file ngoài "Files" của card. Thấy cần → ghi vào PR, đừng tự mở
   rộng scope.
7. **Single-card mode** (một agent, một card): không commit, để diff ở working
   tree. **Lane mode** (5 agent song song): một commit một card trên branch của
   lane, không push — xem § Dispatch protocol.

### Không làm

- Không đổi `domain.Source` signature.
- Không thêm dependency mới nếu stdlib/dep sẵn có làm được. `go.mod` hiện chỉ
  có bubbletea/lipgloss/bubblezone/vt10x/client-go.
- Không refactor "tiện tay".
- Không commit, không push, không tag. Để diff ở working tree.

---

## Board

### P0 — trust & correctness

- [ ] **T01** kind-cluster e2e trong CI
- [ ] **T02** container picker cho logs / shell / top
- [ ] **T03** port-forward chọn container + port
- [ ] **T04** events của object đang chọn
- [ ] **T05** CLI flags
- [ ] **T06** read-only mode + prod context guard

### P1 — daily driver

- [x] **T07** sort theo cột
- [ ] **T08** multi-select + bulk action
- [ ] **T09** log: grep / previous / timestamps / save
- [ ] **T10** port-forward manager
- [ ] **T11** secret decode
- [ ] **T12** saved views
- [ ] **T13** label / field selector

### P2 — differentiator

- [ ] **T14** owner tree (xray)
- [ ] **T15** AI v2 — stream + auto-context + redact
- [ ] **T16** pulse dashboard
- [ ] **T17** `can-i` / RBAC introspect
- [ ] **T18** diff trước khi apply
- [ ] **T19** helm releases
- [ ] **T20** ephemeral debug container + node shell

### P3 — security / polish / distribution

- [ ] **T21** cosign verify cho self-update
- [ ] **T22** API key vào OS keychain
- [ ] **T23** custom keybindings + custom columns
- [ ] **T24** export CSV/JSON + clipboard OSC 52
- [ ] **T25** packaging: brew / scoop / nix
- [ ] **T26** kinds còn thiếu

### P4 — lenses (ecosystem operators)

Spec: [lenses.md](lenses.md). Một cơ chế, năm file YAML — không phải năm feature.
T27→T30 là cơ chế; T31→T35 là data; T36 là thứ chưa TUI nào có.

- [ ] **T27** lens schema + loader (`internal/lens`)
- [ ] **T28** dynamic kind registry — discovery gate + informer per GVR
- [ ] **T29** JSONPath columns + severity sort (worst-first)
- [ ] **T30** declarative actions: 5 verb + confirm modal + ack watch
- [ ] **T31** lens: argocd
- [ ] **T32** lens: cnpg
- [ ] **T33** lens: longhorn
- [ ] **T34** lens: kargo
- [ ] **T35** lens: traefik (spec-only + router inspector opt-in)
- [ ] **T36** edges — điều hướng quan hệ (pod → cnpg Cluster → PVC → longhorn Volume)

### P5 — main-view redesign

Spec: [../SPEC.md](../SPEC.md). Build in numbered order. T37 and T38 come
first because they are the measurement: without them, no later card can show
it did not slow the frame down.

- [x] **T37** frame memo — one `Rows()` per frame
- [x] **T38** fix the perf guards so they measure navigation
- [x] **T39** layout budget — header and side panes by terminal width
- [x] **T40** column policy — weight, priority, measured in cells
- [x] **T41** honest columns — retire the ambiguous `-`
- [x] **T42** row groups — group by owner, on by default for Pods
- [x] **T43** view engine — **not extracted** (see the card); the two fixes it carried are done
- [x] **T44** metric history + bar + sparkline + braille chart panel (`:chart`)
- [x] **T45** action search in the palette + typed gate on Delete/Drain
- [x] **T46** nested owner tree in the main table (opt-in, `t` — `T` is the theme cycler)

### Lanes

Năm lane, mỗi lane một agent, mỗi lane sở hữu một vùng file. Trong lane chạy
**tuần tự** (deps); giữa các lane chạy **song song**.

| Lane | Sở hữu | Thứ tự card |
|---|---|---|
| **A** cluster backend | `internal/k8s/`, `internal/mock/`, `.github/workflows/e2e.yml` | T01 → T13 → T26 → T03 → T10 → T17 → T19 |
| **B** table & render | đường vẽ table trong `internal/ui/view.go` | T07 → T08 → T18 → T24 |
| **C** streams | logs / exec / shell / port-forward stream | T02 → T09 → T20 |
| **D** shell, config, release | `main.go`, `internal/config/`, `internal/update/` | T05 → T06 → T22 → T12 → T23 → T21 → T25 |
| **E** view mới & AI | file mới (`pulse.go`, `treeview.go`), `internal/ai/` | T04 → T11 → T16 → T14 → T15 |
| **F** lenses | `internal/lens/`, `internal/k8s/lens*.go` | T27 → T28 → T29 → T30 → T31 → T32 → T33 → T34 → T35 → T36 |

Deps cắt ngang lane — có ba, và cả ba đều nằm **cuối** lane cần chúng:

```
C:T02 (container picker)  →  A:T03, A:T10
B:T07 (sort state)        →  D:T12, D:T23
D:T06 (cơ chế ẩn action)  →  A:T17
```

Card bị chặn mà dep chưa merge → **bỏ qua, làm card kế tiếp, báo cáo là hoãn**.
Không tự implement dep của lane khác.

Lane **F** chạy sau cùng và đụng vùng của lane khác đúng hai chỗ — cả hai đều
phải theo luật "chỉ được thêm" ở § Dispatch protocol:

```
F:T28  →  internal/k8s/store.go  (vùng lane A): Kinds() merge lens kind,
          gvrFor() nhận key của lens. Append, không sửa nhánh builtin.
F:T36  →  internal/ui/treeview.go (vùng lane E): panel quan hệ.
          Chờ E:T14 (owner tree) merge xong rồi mới làm.
```

Thứ tự merge đầy đủ: `C → B → D → A → E → F`.

### Dispatch protocol

Chạy 5 agent song song trên cùng một repo sẽ đụng nhau ở `model.go`,
`domain.go`, `actions.go`. Luật để việc đó không thành hỗn loạn:

1. **Mỗi lane một branch**: `lane/a-backend`, `lane/b-table`, `lane/c-streams`,
   `lane/d-shell`, `lane/e-views`. Tạo từ `main`.
2. **Một commit một card**, message `T07: sort theo cột`. Không push, không tag.
   (Đây là chỗ lane mode khác single-card mode ở § Definition of done.)
3. **Rebase lên `main` trước mỗi card mới.** Card sau của lane phải đứng trên
   thứ các lane khác vừa merge.
4. **File dùng chung — chỉ được thêm, không được sắp xếp lại:**
   - `internal/domain/domain.go`: interface mới **append cuối file**, một block
     một card, kèm comment card ID. Không sửa `Source`.
   - `internal/ui/model.go`: field mới **append cuối struct `Model`**, một block
     một card. Không đổi thứ tự field cũ.
   - `internal/ui/actions.go`: append vào cuối slice `Actions`.
   - `internal/mock/`: append. Không sửa row đã có (test khác đang assert nó).
   - Không `go mod tidy`, không đổi `go.mod`, trừ khi card nói rõ.
5. **Thứ tự merge**: `C → B → D → A → E`. Đây là thứ tự tô-pô của ba dep cắt
   ngang ở trên — A merge sau D vì `A:T17` cần `D:T06`.
6. Lane nào đụng file ngoài vùng sở hữu của mình → dừng, báo dispatcher.

---

# P0

## T01 — kind-cluster e2e trong CI

**Effort** M · **Deps** none · **Lane** A

**Goal** — CI chạy k10s thật với `kind` cluster, không chỉ fake clientset.

**Why** — `docs/roadmap.md` § Known limits tự nhận: "Untested against a real
cluster by its author". Toàn bộ live path (`internal/k8s`) chỉ được test bằng
`k8s.io/client-go/kubernetes/fake`, thứ không bắt được RBAC, discovery,
API version drift, SPDY, hay exec. Đây là rủi ro số 1 của repo — mọi card sau
đều đứng trên giả định "live path chạy được".

**Files**

- `.github/workflows/e2e.yml` (mới)
- `internal/k8s/e2e_test.go` (mới, build tag `//go:build e2e`)
- `Justfile` — thêm recipe `test-e2e`
- `docs/dev.md` § Tests

**Design**

- `helm/kind-action@v1` dựng cluster, apply một manifest fixture
  (`internal/k8s/testdata/e2e.yaml`: 1 Deployment 2 replica, 1 Service,
  1 ConfigMap, 1 Secret, 1 Job, 1 CrashLoopBackOff pod).
- Test build tag `e2e` → `just test` thường **không** chạy nó, CI gọi
  `go test -tags e2e ./internal/k8s/...`.
- Cover, mỗi cái một test: `NewStore` connect thật · `Rows` cho ≥8 kind ·
  `RowCount` · `Describe` · `YAML` · `LogsTail` · `LogsFollow` (nhận ≥1 dòng
  rồi `stop()`) · `Scale` · `Restart` · `Delete` · `PortForward` (dial được
  localAddr rồi stop) · `ShellSession` (chạy `echo hi`, đọc lại được).
- Metrics-server không có trong kind mặc định → `TopPod`/`TopNode` phải fail
  **gracefully**, assert đúng error chứ không panic.

**Accept**

- [ ] `just test` (không tag) vẫn không cần cluster, thời gian không đổi.
- [ ] `go test -tags e2e ./internal/k8s/...` xanh trên kind.
- [ ] Workflow chạy trên push `main` + PR, matrix ≥2 k8s version
      (oldest supported + latest).
- [ ] Bất kỳ path nào panic với real API server → test đỏ, không phải TUI chết.
- [ ] `docs/roadmap.md` § Known limits xoá dòng "Untested against a real cluster".

**Not** — không đổi production code trừ khi e2e phát hiện bug thật; bug tìm
được thì ghi riêng thành card mới.

---

## T02 — container picker cho logs / shell / top

**Effort** M · **Deps** none · **Lane** C

**Goal** — pod nhiều container: người dùng chọn container, không bị ép
`Containers[0]`.

**Why** — `internal/k8s/logs.go:18 podContainer()` luôn trả
`p.Spec.Containers[0].Name`. Với istio-proxy / vault-agent / linkerd sidecar,
`Containers[0]` thường **không** phải app → user đọc log sai container mà không
hề biết. Đây là bug thầm lặng, tệ hơn crash.

**Files**

- `internal/domain/domain.go` — optional interface `Containers`
- `internal/k8s/logs.go`, `internal/k8s/shell.go`, `internal/k8s/exec.go`,
  `internal/k8s/metrics_top.go`
- `internal/mock/source.go`, `internal/mock/data.go`
- `internal/ui/pickers.go`, `internal/ui/pickers_view.go`, `internal/ui/model.go`
- `docs/keybindings.md`

**Design**

```go
type Containers interface {
    // Containers lists container names of the pod this object resolves to,
    // init and ephemeral containers included, app containers first.
    Containers(kind, ns, name string) ([]string, error)
}
type LogsInContainer interface {
    LogsTailIn(kind, ns, name, container string, n int) ([]string, bool, error)
    LogsFollowIn(kind, ns, name, container string) (<-chan string, func(), error)
}
```

- 1 container → **không** hiện picker, hành vi y như cũ. Đây là case đa số,
  đừng bắt họ thêm một cú enter.
- ≥2 container → popup picker dùng lại pattern của `ctxpicker.go` (đã có filter,
  đã clickable). Container đã chọn nhớ theo `(ns,pod)` trong `Model`, không
  persist ra config.
- Tiêu đề log panel hiện `logs -f <pod> · <container>`.
- `c` cycle container ngay trong log view, không cần đóng mở lại.

**Accept**

- [ ] Pod 3 container → `l` mở picker, chọn container thứ 2 → log đúng của nó.
- [ ] Pod 1 container → `l` mở thẳng log, không popup.
- [ ] `s` (shell) và `m` (top) dùng chung container đã chọn.
- [ ] Backend không impl `Containers` → không có picker, không crash.
- [ ] Mock backend có pod đa container để `just shot` demo được.

**Tests** — `internal/k8s/logs_test.go`: pod 3 container, assert thứ tự và
`LogsTailIn` gọi đúng container. `internal/ui`: assert picker chỉ mở khi ≥2.

**Not** — không làm multi-container tail gộp (`--all-containers`); card riêng.

---

## T03 — port-forward chọn container + port

**Effort** S · **Deps** T02 (dùng lại picker) · **Lane** A

**Goal** — forward được port bất kỳ, không chỉ port đầu tiên của container đầu.

**Why** — `internal/k8s/portforward.go:76` hardcode
`p.Spec.Containers[0].Ports[0].ContainerPort`, và bỏ qua pod nào
`Containers[0]` không khai báo port (dòng 73) — pod có sidecar mesh gần như
luôn rơi vào case này. Service multi-port cũng chỉ forward được port đầu.

**Files** — `internal/k8s/portforward.go`, `internal/domain/domain.go`,
`internal/mock/source.go`, `internal/ui/pickers.go`

**Design**

- Optional interface `PortSource { Ports(kind, ns, name string) ([]Port, error) }`,
  `Port{Container, Name string; Number int32; Protocol string}`.
- Gom port của **mọi** container, không chỉ `[0]`.
- 1 port → forward luôn. ≥2 → picker, hiện `container · name · number`.
- Cho phép chỉ định local port: `p` = auto (:0), `shift+p` = hỏi local port.
- Service: resolve qua endpoints → pod, giữ nguyên hành vi hiện tại nhưng
  liệt kê đủ port của service.

**Accept**

- [ ] Pod mà `Containers[0]` không có port, `Containers[1]` có → forward được.
- [ ] Pod 3 port → picker, chọn cái nào forward đúng cái đó.
- [ ] Local port cụ thể bị chiếm → error rõ ràng, không treo.

---

## T04 — events của object đang chọn

**Effort** S · **Deps** none · **Lane** E

**Goal** — `shift+e` trên bất kỳ row nào → chỉ events của object đó, mới nhất trên.

**Why** — debug k8s thật sự bắt đầu ở events ("FailedScheduling",
"ImagePullBackOff", "OOMKilled"). Hiện `:ev` chỉ list toàn namespace, user phải
tự dò cột OBJECT. Backend đã có sẵn: `internal/k8s/rows.go:605` đã đọc
`e.InvolvedObject`, chỉ thiếu filter + đường vào UI.

**Files** — `internal/k8s/rows.go`, `internal/domain/domain.go`,
`internal/ui/actions.go`, `internal/ui/model.go`, `internal/mock/extra.go`,
`docs/keybindings.md`, README

**Design**

- Optional interface `ObjectEvents { EventsFor(kind, ns, name string) (cols []string, rows [][]string, err error) }`.
- Match theo `InvolvedObject.Kind` + `Name` (+ `UID` nếu có, để không dính pod
  cũ trùng tên).
- Deployment → gộp cả events của RS và Pod nó sở hữu. Đây là điểm khác biệt
  thật so với `kubectl describe`: lỗi của Deployment gần như luôn nằm ở Pod.
- Thêm action `{domain.AEvents, "E", "Events", "󰀦", false}`, cho **mọi** kind.
- Warning events tô màu `theme.Danger`; sort mới nhất trước.
- Rỗng → "no events for this object in the last hour" (events có TTL), không
  phải bảng trống.

**Accept**

- [ ] `shift+e` trên pod → chỉ events của pod đó.
- [ ] `shift+e` trên deployment → events của deploy + RS + pods.
- [ ] Object không có event → thông điệp giải thích TTL.
- [ ] Action hiện trong pane cho mọi kind.

---

## T05 — CLI flags

**Effort** S · **Deps** none · **Lane** D

**Goal** — `k10s -n kube-system --context prod po` mở thẳng đúng chỗ.

**Why** — `main.go:40` chỉ nhận `update` / `version` / `help`. Không script
được, không alias được, không dùng trong tmux layout được. Mọi TUI k8s đều có,
và đây là thứ chặn người dùng ngay trước khi họ thấy feature nào khác.

**Files** — `main.go`, `internal/ui/model.go` (`Startup` struct),
`docs/install.md`, README § Install

**Design**

Dùng `flag` stdlib, **không** thêm cobra.

```
k10s [flags] [resource]

  -n, --namespace   namespace mở lúc đầu ("all" cũng được)
      --context     kube context, ghi đè current-context
      --kubeconfig  đường dẫn kubeconfig
      --theme       theme cho phiên này, không ghi vào config
      --readonly    ẩn mọi action phá huỷ (xem T06)
  -h, --help  -v, --version
```

- Positional arg = alias resource, đi qua đúng `kindForAlias`
  (`internal/ui/commands.go:182`) mà `:po` dùng — một nguồn sự thật.
- Precedence: flag > env (`KUBECONFIG`) > config.yaml > kubeconfig
  current-context. Ghi bảng này vào `docs/config.md`.
- Flag **không** ghi đè config file. Nó là override cho phiên chạy.
- Alias sai → in ra list alias hợp lệ rồi `exit 2`, đừng mở TUI rồi mới báo.

**Accept**

- [ ] `k10s -n kube-system po` mở Pods ở kube-system.
- [ ] `k10s --context nonexistent` báo lỗi rõ, exit 1, không mở TUI.
- [ ] `k10s --theme dracula` không sửa `~/.k10s/config.yaml`.
- [ ] `k10s` trần vẫn y hệt hành vi cũ.
- [ ] `--help` liệt kê đủ flag.

---

## T06 — read-only mode + prod context guard

**Effort** M · **Deps** T05 · **Lane** D

**Goal** — không xoá nhầm production bằng một cú click.

**Why** — grep `readonly` trong `internal/` → 0 hit. k10s bán điểm mạnh là
"click được", nhưng `D` delete và `u` drain cũng click được, cách row bạn định
chọn đúng một pixel. Một TUI mouse-first mà không có phanh thì tai nạn chỉ là
vấn đề thời gian. k9s có `readOnly` từ lâu; đây là bảng cân đối cho tính năng
chủ đạo của repo.

**Files** — `internal/config/config.go`, `internal/ui/actions.go`,
`internal/ui/model.go`, `internal/ui/view.go`, `internal/plugin/plugin.go`,
`main.go`, `docs/config.md`, README

**Design**

Config mới (nhớ sửa **cả** `render()` và `parse()`):

```yaml
readonly: false
danger_contexts: "prod,production,*-prod"   # glob, khớp tên context
```

- Read-only → action `Risky` (`ADelete`, `ADrain`) và cả `AEdit`, `AScale`,
  `ARestart`, `ACordon` biến mất khỏi Actions pane, phím tương ứng thành no-op
  kèm toast "read-only mode". **Ẩn hẳn**, không phải hiện rồi báo lỗi — pane này
  là bản hợp đồng "đây là những gì bạn làm được".
- Plugin có `dangerous: true` cũng bị chặn. Plugin bypass được thì read-only
  chỉ là trang trí.
- Context khớp `danger_contexts` → banner đỏ liên tục trên header, và modal
  confirm của action phá huỷ bắt **gõ tên object** để xác nhận, không phải chỉ
  enter.
- `--readonly` bật cho phiên; `:ro` toggle trong phiên (chỉ khi config không
  ép `readonly: true`).

**Accept**

- [ ] `--readonly` → `D` không xoá, Actions pane không hiện Delete.
- [ ] Context `prod` → banner đỏ, delete bắt gõ tên.
- [ ] Plugin `dangerous: true` bị chặn ở read-only.
- [ ] Config `readonly: true` thì `:ro` không tắt được.
- [ ] `just shot` chụp được cả hai trạng thái.

---

# P1

## T07 — sort theo cột

**Effort** M · **Deps** none · **Lane** B

**Goal** — click header hoặc `shift+<n>` để sort, `RESTARTS` giảm dần là một
cú click.

**Why** — hiện chỉ có natural A→Z (`internal/k8s/rows.go:87 sortRows`).
Câu hỏi hay gặp nhất — "pod nào restart nhiều nhất", "node nào CPU cao nhất",
"gì vừa thay đổi" — đều là sort theo cột và đều không làm được.

**Files** — `internal/ui/view.go`, `internal/ui/model.go`, `internal/domain/domain.go`

**Design**

- Sort ở **UI**, không ở backend. Backend giữ nguyên order ổn định; UI sort bản
  đã filter. Sort ở backend sẽ đụng cache và `RowCount`.
- State: `sortCol int` (-1 = mặc định), `sortDesc bool`, nhớ **theo kind**
  (`map[string]sortState`) — sort của Pods không nên áp cho Services.
- Click lần 1 = asc, lần 2 = desc, lần 3 = về mặc định.
- Comparator tự nhận kiểu theo giá trị cột: số (`RESTARTS`, `CPU`), duration
  (`AGE`: `5d2h`), quantity (`100Mi`, `2Gi`), còn lại `domain.NaturalLess`.
  Viết thành `domain.CompareCell(a, b string) int` để test riêng được.
- Header cột đang sort hiện `▲`/`▼`, zone bubblezone cho từng header.
- Events giữ newest-first làm mặc định như hiện tại.

**Accept**

- [ ] Click `RESTARTS` 2 lần → nhiều nhất lên đầu.
- [ ] `AGE` sort theo thời gian thật, không theo chuỗi (`10m` < `2h` < `3d`).
- [ ] `2Gi` > `900Mi`.
- [ ] Đổi kind rồi quay lại → giữ sort của kind đó.
- [ ] `TestViewDoesNotBuildRows` vẫn xanh.

**Tests** — bảng test cho `CompareCell` phủ: số, duration, quantity, chuỗi,
ô rỗng, `<none>`.

---

## T08 — multi-select + bulk action

**Effort** M · **Deps** T07 · **Lane** B

**Goal** — `space` mark nhiều row, một lần `D` xử cả set.

**Why** — dọn 12 pod Evicted hiện là 12 lần `D` + 12 lần confirm.

**Files** — `internal/ui/model.go`, `internal/ui/view.go`, `internal/ui/actions.go`

**Design**

- `marked map[string]bool` khoá `ns/name`, **xoá sạch khi đổi kind hoặc
  namespace** — mark tàng hình xuyên view là cách xoá nhầm.
- `space` toggle, `ctrl+a`… đã dùng cho AI → dùng `*` để mark-all-filtered,
  `esc` clear.
- Row đã mark: gutter đổi màu + `▌` bên trái. Status bar hiện `3 marked`.
- Bulk chạy **tuần tự**, có progress, gặp lỗi thì tiếp tục phần còn lại và
  cuối cùng báo tổng kết "9 deleted, 3 failed" + list lỗi.
- Confirm modal liệt kê **đủ** tên (scroll được), không phải "3 objects".
- Read-only (T06) chặn bulk như chặn single.

**Accept**

- [ ] Mark 3 pod → `D` → 1 confirm, list đủ 3 tên, xoá cả 3.
- [ ] 1 trong 3 fail → 2 kia vẫn xoá, báo cáo chính xác.
- [ ] Đổi namespace → mark biến mất.
- [ ] Không mark gì → `D` vẫn xử row đang chọn như cũ.

---

## T09 — log: grep / previous / timestamps / save

**Effort** M · **Deps** T02 · **Lane** C

**Goal** — log view làm được 4 việc `kubectl logs` làm được mà nó chưa.

**Why** — `internal/ui/logview.go` chỉ có follow + scroll. Thiếu nhất là
`--previous`: pod CrashLoopBackOff thì log **hiện tại** rỗng, cái bạn cần nằm ở
container đã chết. Đúng lúc cần nhất thì công cụ không có.

**Files** — `internal/ui/logview.go`, `internal/ui/model.go`,
`internal/k8s/logs.go`, `internal/domain/domain.go`, `internal/mock/source.go`,
`docs/keybindings.md`

**Design**

Phím trong log view:

| phím | việc |
|---|---|
| `/` | grep: chỉ hiện dòng khớp, highlight, `n`/`N` nhảy, hoạt động cả khi đang follow |
| `p` | toggle `--previous` — container đã chết |
| `t` | toggle timestamps |
| `w` | ghi buffer ra `~/k10s-logs/<ns>-<pod>-<ts>.log`, toast đường dẫn |
| `c` | đổi container (T02) |

- Grep là **filter hiển thị**, không cắt buffer — tắt grep phải thấy lại đủ.
  Hỗ trợ regex, regex sai thì báo inline chứ không rơi về substring âm thầm.
- `--previous` mà không có container trước → nói thẳng "no previous container
  (pod has not restarted)", không phải panel trống.
- Đường dẫn file phải sanitize `ns`/`pod`.

**Accept**

- [ ] Grep đang follow: dòng mới không khớp không hiện, tắt grep là thấy lại.
- [ ] Pod restart → `p` ra log của lần chết trước.
- [ ] Pod chưa restart → `p` báo rõ.
- [ ] `w` ghi đúng file, kể cả 5000 dòng.
- [ ] Grep regex sai → báo lỗi inline, không crash.

---

## T10 — port-forward manager

**Effort** S · **Deps** T03 · **Lane** A

**Goal** — `:pf` liệt kê mọi forward đang chạy, stop từng cái hoặc stop hết.

**Why** — forward hiện sống trong pane; đóng pane hoặc đổi view là mất dấu.
Goroutine còn chạy, port còn giữ, không có đường nào nhìn thấy hay tắt trừ
thoát app.

**Files** — `internal/ui/model.go`, `internal/ui/commands.go`, `internal/ui/view.go`

**Design**

- Registry trong `Model`: `[]activeForward{kind, ns, name, container, local, remote, since, stop func()}`.
- View `:pf` là table thường (dùng lại render table sẵn có), action `D` = stop.
- Header hiện `⇄ 2` khi có forward đang chạy — trạng thái vô hình là trạng thái
  bị quên.
- Thoát app → stop hết trong cleanup.
- Forward chết ngoài ý muốn (pod bị xoá) → tự bỏ khỏi registry + toast.

**Accept**

- [ ] 2 forward → `:pf` hiện đủ 2 với local addr.
- [ ] `D` stop đúng cái đó, cái kia vẫn chạy.
- [ ] Xoá pod đang forward → biến khỏi list, có toast.
- [ ] Quit → không còn port nào bị giữ.

---

## T11 — secret decode

**Effort** S · **Deps** none · **Lane** E

**Goal** — `x` trên Secret → xem plaintext, không phải copy đi `base64 -d`.

**Why** — thao tác này ai cũng làm hàng ngày và hiện tại nó khó chịu vô lý.

**Files** — `internal/k8s/yaml.go`, `internal/domain/domain.go`,
`internal/mock/source.go`, `internal/ui/actions.go`, `docs/keybindings.md`

**Design**

- Optional interface `Decoder { Decoded(kind, ns, name string) (string, error) }`.
- Mặc định **che**: `api-key: ••••••••` — `x` một lần nữa mới hiện thật. Ai đó
  share màn hình là chuyện thường; mở ra là hiện secret thì tính năng này thành
  cái bẫy.
- Giá trị binary → hiện `<binary, 2048 bytes>`, không đổ byte rác ra terminal.
- Read-only mode (T06) **không** chặn — đây là đọc. Nhưng ghi log toast
  "revealed" để hành động có dấu vết trên màn hình.
- Copy mode (`ctrl+s`) vẫn dùng được để copy giá trị ra.

**Accept**

- [ ] `x` trên Secret → key + giá trị đã che.
- [ ] `x` lần 2 → giá trị thật.
- [ ] Secret binary (`dockerconfigjson`) → không phá layout.
- [ ] Kind khác Secret → không có action `x`.

---

## T12 — saved views

**Effort** M · **Deps** T07 · **Lane** D

**Goal** — `kind + ns + filter + sort` lưu thành tên, `1`–`9` nhảy tới.

**Why** — mỗi người có 3–5 chỗ hay xem ("pod lỗi ở prod", "ingress ở staging").
Hiện mỗi lần phải dựng lại bằng tay.

**Files** — `internal/config/` (file mới `views.go`), `internal/ui/model.go`,
`internal/ui/commands.go`, `docs/config.md`

**Design**

- **File riêng** `~/.k10s/views.yaml`. Parser trong `config.go` là flat, không
  đọc được list-of-map — đừng cố nhồi vào.
- Vẫn không thêm dep YAML: format tự định nghĩa, một view một khối, parser
  nhỏ + test.

  ```yaml
  views:
    - key: "1"
      name: "prod failures"
      kind: pods
      namespace: all
      filter: "Error|CrashLoop"
      sort: "RESTARTS:desc"
      context: prod
  ```

- `:save <name>` lưu view hiện tại vào slot trống tiếp theo; `:views` mở picker;
  `1`–`9` nhảy trực tiếp (chỉ khi table đang focus, không phải đang gõ).
- `context` optional: có thì switch context luôn.
- Kind/context trong file không còn tồn tại → bỏ qua view đó + cảnh báo, không
  làm hỏng startup.

**Accept**

- [ ] `:save prod-fail` → `~/.k10s/views.yaml` có entry, restart vẫn còn.
- [ ] `1` khôi phục đủ kind + ns + filter + sort.
- [ ] File hỏng → app vẫn khởi động, có cảnh báo.
- [ ] `1` khi đang gõ trong prompt thì gõ ra "1", không nhảy view.

---

## T13 — label / field selector

**Effort** S · **Deps** none · **Lane** A

**Goal** — `:po -l app=api` và `:po --field-selector status.phase=Running`.

**Why** — label selector là cách người ta thật sự nghĩ về workload. Filter chuỗi
hiện tại chỉ khớp text đang hiển thị, không đụng được tới label.

**Files** — `internal/ui/commands.go`, `internal/k8s/rows.go`,
`internal/domain/domain.go`, `internal/mock/source.go`

**Design**

- Optional interface `Selector { RowsSelected(kind, ns string, sel Selection) (cols []string, rows [][]string) }`,
  `Selection{Labels, Fields string}`.
- Label filter chạy **trên lister cache** bằng `labels.Parse` — không thêm API
  call, không phá guard performance.
- Field selector: chỉ hỗ trợ tập k8s thật sự hỗ trợ (`status.phase`,
  `spec.nodeName`, `metadata.name`). Field không hỗ trợ → báo rõ chứ đừng im
  lặng trả sai.
- Selector đang bật → hiện chip trong header, `esc` xoá.

**Accept**

- [ ] `:po -l app=api` chỉ ra pod có label đó.
- [ ] `-l 'env in (prod,staging)'` chạy đúng (`labels.Parse` lo).
- [ ] Selector sai cú pháp → lỗi rõ, không bảng trống bí ẩn.
- [ ] Không thêm request nào tới API server.

---

# P2

## T14 — owner tree (xray)

**Effort** L · **Deps** none · **Lane** E

**Goal** — `ctrl+r` mở cây `Deployment → ReplicaSet → Pod → Container`, click
được từng nút.

**Why** — đây là feature hợp gu k10s nhất: quan hệ sở hữu vốn là cây, mà bảng
phẳng thì che nó đi. "Deploy này đang chạy pod nào, và pod nào của RS cũ" là
câu hỏi thường xuyên mà hôm nay phải trả lời bằng cách đối chiếu 3 bảng.

**Files** — `internal/k8s/tree.go` (mới), `internal/domain/domain.go`,
`internal/mock/extra.go`, `internal/ui/treeview.go` (mới),
`internal/ui/model.go`, `internal/ui/view.go`

**Design**

```go
type Tree interface {
    Tree(kind, ns, name string) (*domain.Node, error)
}
type Node struct {
    Kind, Name, Status string
    Healthy            bool
    Children           []*Node
}
```

- Xuống: theo `OwnerReferences` ngược (`internal/k8s/actions.go` đã đọc
  ownerRef, dùng lại).
- Lên: từ pod đi ngược lên deploy — cùng một hàm, chỉ khác điểm bắt đầu.
- Service → endpoints → pods cũng là quan hệ đáng vẽ, dù không phải ownerRef.
- Nút không khoẻ tô `theme.Danger`; mặc định mở hết, `space` gập.
- `enter` trên nút = mở object đó trong main panel như bảng thường.
- Cây tính **on demand**, không tự refresh — không được đụng render path.

**Accept**

- [ ] `ctrl+r` trên Deployment → RS → Pods, số lượng khớp bảng.
- [ ] `ctrl+r` trên Pod → đi ngược lên tận Deployment.
- [ ] Pod CrashLoop → nút đỏ, cha cũng có dấu hiệu.
- [ ] Cây >200 nút vẫn render mượt.
- [ ] Không phá `TestKeypressLatency`.

---

## T15 — AI v2: stream + auto-context + redact

**Effort** L · **Deps** none · **Lane** E

**Goal** — AI từ "one-shot có biết tên pod" thành "thật sự đã xem object đó".

**Why** — hiện `internal/ai` chỉ nhét context/ns/kind/tên vào prompt rồi chờ
full response. Model không thấy describe, không thấy events, không thấy log —
tức là không thấy đúng những thứ trả lời được câu hỏi. Và prompt đang gửi đi
**chưa lọc secret**.

**Files** — `internal/ai/ai.go`, `internal/ai/redact.go` (mới),
`internal/ui/model.go`, `internal/ui/view.go`, `docs/commands.md`

**Design**

Bốn phần, làm được theo thứ tự này:

1. **Redact trước tiên** (bắt buộc, không optional). Trước khi gửi bất cứ gì:
   lọc giá trị Secret, `Authorization:` header, JWT (`eyJ…`), AWS key
   (`AKIA…`), private key block, `password=`/`token=`. Có test riêng cho từng
   pattern. Chưa xong bước này thì không merge 3 bước sau.
2. **Stream** — SSE cho cả OpenAI-compatible lẫn Anthropic, token hiện dần.
   `esc` huỷ giữa chừng.
3. **Auto-context** — `ctrl+a` khi đang chọn một object → gom
   `describe` + events (T04) + `logs --tail=100` + YAML đã rút gọn, kèm ngân
   sách token; vượt thì cắt log trước, YAML sau. Hiện cho user **chính xác**
   những gì sắp gửi + bytes, có nút huỷ. Không bao giờ gửi lén.
4. **Follow-up** — giữ history trong phiên, `ctrl+a` lần nữa là hỏi tiếp chứ
   không phải hỏi lại từ đầu. `:ai clear` xoá.

**Accept**

- [ ] Secret trong describe → không xuất hiện trong payload (test bằng
      httptest server, assert body).
- [ ] Response hiện dần, `esc` huỷ được.
- [ ] Preview context hiện đúng bytes trước khi gửi.
- [ ] Follow-up nhớ câu trước.
- [ ] Không có API key → AI mode tắt như cũ, không lỗi.

**Not** — chưa làm "apply patch AI đề xuất". Card riêng, sau T18.

---

## T16 — pulse dashboard

**Effort** M · **Deps** none · **Lane** E

**Goal** — mở k10s ra thấy **tình trạng cluster** ngay, không phải một bảng Pods.

**Why** — hôm nay màn hình đầu tiên là Pods của một namespace, thứ chưa trả lời
câu hỏi nào. Câu hỏi thật lúc mở app là "có gì cháy không".

**Files** — `internal/ui/pulse.go` (mới), `internal/ui/model.go`,
`internal/k8s/counts.go`, `internal/domain/domain.go`

**Design**

Ô, click được, mỗi ô nhảy tới view tương ứng:

```
nodes 4/4 ready · pods 128 (3 not ready, 1 crashloop)
restarts trong 1h: 7  · warning events: 12
pvc >80%: 2 · pod pending: 1 · job failed: 0
```

- Dữ liệu lấy từ **counts pass đã có** (`internal/k8s/counts.go`), không mở
  watch mới. Ràng buộc này không thương lượng — nó là lý do k10s khởi động
  nhanh.
- Refresh theo tick sẵn có, không tick riêng.
- Số nào chưa biết → hiện `—`, không hiện `0`. (Xem `domain.CountUnknown`.)
- Bật/tắt bằng `--home pulse|pods` (T05) + config `home:`.

**Accept**

- [ ] Startup vẫn 0 watch; `TestNewStoreReturnsFast` xanh.
- [ ] Click "3 not ready" → Pods đã filter sẵn.
- [ ] Cluster chưa sync → `—`, không phải `0`.
- [ ] `--home pods` giữ hành vi cũ.

---

## T17 — `can-i` / RBAC introspect

**Effort** M · **Deps** T06 · **Lane** A

**Goal** — action nào bạn không có quyền thì không hiện, thay vì click xong mới
ăn 403.

**Why** — Actions pane là lời hứa "đây là những gì bạn làm được với thứ này".
Với token bị giới hạn, lời hứa đó đang sai.

**Files** — `internal/k8s/access.go` (mới), `internal/domain/domain.go`,
`internal/ui/actions.go`, `internal/mock/extra.go`

**Design**

- `SelfSubjectAccessReview` cho `(verb, resource, ns)`.
- **Cache theo (verb, resource, ns)**, TTL 5 phút. Không được gọi SSAR trong
  render path — pane vẽ mỗi frame.
- Warm bất đồng bộ khi đổi kind/ns; chưa biết thì **hiện** action (fail-open).
  Fail-closed sẽ làm action nhấp nháy biến mất lúc mới mở, tệ hơn nhiều.
- `:cani <verb> <resource>` in kết quả trực tiếp.
- Bị từ chối → tooltip "forbidden: needs delete on pods".

**Accept**

- [ ] Token chỉ đọc → Delete/Edit/Scale không hiện.
- [ ] Không SSAR nào được gọi từ render path (test guard).
- [ ] Cache: đổi kind qua lại không tạo request mới trong TTL.
- [ ] SSAR lỗi → fail-open, action vẫn hiện.

---

## T18 — diff trước khi apply

**Effort** M · **Deps** none · **Lane** B

**Goal** — `e` edit → thấy diff server-side dry-run → mới confirm.

**Why** — `Apply` hiện ghi thẳng. Sửa YAML trong `$EDITOR` rồi ghi mù vào
cluster là chỗ dễ mất `resourceVersion`, dễ ghi đè thay đổi của người khác.

**Files** — `internal/k8s/actions.go`, `internal/domain/domain.go`,
`internal/ui/model.go`, `internal/ui/diffview.go` (mới)

**Design**

- Optional interface `Differ { Diff(kind, ns, name, yaml string) (string, error) }`
  dùng server-side apply `dryRun=All`, so với object hiện tại.
- Diff màu, `+`/`-`, chỉ hiện field đổi, bỏ qua `managedFields`/
  `resourceVersion`/`generation` — nhiễu che mất thứ thật.
- Không đổi gì → "no changes", không apply.
- Conflict (object đã đổi từ lúc mở editor) → báo, cho chọn reload hoặc force.

**Accept**

- [ ] Sửa replicas → diff chỉ hiện dòng đó.
- [ ] Không sửa gì → "no changes", không gọi apply.
- [ ] Object bị người khác đổi giữa chừng → cảnh báo conflict.
- [ ] Read-only → không vào được edit.

---

## T19 — helm releases

**Effort** L · **Deps** none · **Lane** A

**Goal** — kind mới `:helm`: list / history / values / rollback.

**Why** — phần lớn thứ trong cluster do helm cài. Xem được release mà không rời
k10s là khoảng cách rõ ràng với `kubectl`-only.

**Files** — `internal/k8s/helm.go` (mới), `internal/k8s/kinds.go`,
`internal/mock/data.go`, `internal/domain/domain.go`, `docs/commands.md`

**Design**

- **Không** import SDK `helm.sh/helm/v3` — nó kéo theo cả rừng dependency. Đọc
  thẳng release secret (`type=helm.sh/release.v1`), base64 + gzip + JSON. Format
  ổn định từ Helm 3.
- Cột: `NAME · NAMESPACE · REVISION · STATUS · CHART · APP VERSION · UPDATED`.
- Action: `y` values · `h` history · `R` rollback (risky, chịu T06) ·
  `d` describe.
- Rollback thì shell ra `helm` binary nếu có trên PATH; không có thì ẩn action
  và nói lý do. Tự cài đặt lại release bằng tay là sai.

**Accept**

- [ ] Release cài bằng helm → hiện đúng revision + status.
- [ ] `h` ra history đủ các revision.
- [ ] Không có helm binary → rollback ẩn, có giải thích.
- [ ] Cluster không có release nào → bảng rỗng, không lỗi.

---

## T20 — ephemeral debug container + node shell

**Effort** M · **Deps** T02 · **Lane** C

**Goal** — shell được vào pod distroless và vào node.

**Why** — `s` hôm nay hỏng với image distroless/scratch (không có shell) —
đúng loại image mà production hay dùng.

**Files** — `internal/k8s/exec.go`, `internal/k8s/shell.go`,
`internal/domain/domain.go`, `internal/ui/actions.go`

**Design**

- Pod: `shift+s` → ephemeral container (`kubectl debug` equivalent qua
  subresource `ephemeralcontainers`), image mặc định
  `busybox:1.36` đổi được qua config `debug_image`.
- Node: `s` trên Node → pod privileged `nsenter` trên node đó, tự dọn khi
  đóng session.
- Cả hai đều **risky** → confirm + chịu read-only (T06).
- Cluster không cho ephemeral container (feature gate tắt) → báo rõ, không
  treo.
- Pod debug tạo ra phải dọn kể cả khi k10s bị kill — đặt label
  `k10s.io/debug=true` và dọn ở startup lần sau.

**Accept**

- [ ] Pod distroless → `shift+s` vào được shell.
- [ ] `s` trên node → shell trên node.
- [ ] Đóng session → pod debug biến mất.
- [ ] k10s bị kill giữa chừng → lần chạy sau dọn nốt.
- [ ] Read-only → cả hai bị chặn.

---

# P3

## T21 — cosign verify cho self-update

**Effort** M · **Deps** none · **Lane** D

**Goal** — update kiểm chữ ký, không chỉ checksum.

**Why** — `docs/roadmap.md` tự ghi: checksum chứng minh file khớp với thứ
release công bố, **không** chứng minh ai công bố. k10s tự ghi đè binary đang
chạy — đây là đường tấn công đắt giá nhất trong repo. Nên làm **trước khi** có
người ngoài dùng, không phải sau.

**Files** — `internal/update/verify.go` (mới), `internal/update/update.go`,
`.github/workflows/release.yml`, `Justfile`, `docs/update.md`

**Design**

- Release ký bằng cosign keyless (OIDC GitHub Actions) → `.sig` + `.pem` cạnh
  archive.
- Verify **trong** binary, không shell ra `cosign`. Pin identity
  (`repo == 0x01001011/k10s`, issuer GitHub).
- Không có chữ ký (release cũ) → cảnh báo rõ + hỏi, không im lặng cho qua và
  cũng không cấm cứng.
- `K10S_UPDATE_REPO` trỏ fork → identity đổi theo repo đó, và nói rõ cho user.

**Accept**

- [ ] Archive bị sửa → verify fail, không cài.
- [ ] Chữ ký của repo khác → fail.
- [ ] Release chưa ký → cảnh báo + hỏi.
- [ ] `RealDist` test vẫn xanh.

---

## T22 — API key vào OS keychain

**Effort** M · **Deps** none · **Lane** D

**Goal** — key AI không nằm plaintext trong `config.yaml`.

**Why** — roadmap đã liệt kê là known limit. `~/.k10s/config.yaml` hay bị
dotfile-sync lên git.

**Files** — `internal/config/secret_darwin.go`, `_linux.go`, `_windows.go`
(mới), `internal/config/config.go`, `internal/ui/settings.go`

**Design**

- Giữ nguyên field `AI.APIKey` trong struct — UI không đổi.
- Backend: macOS `security`, Linux `secret-tool` (libsecret), Windows
  credential manager. Không có → fallback plaintext như hiện tại **kèm cảnh báo
  hiện rõ trong `/settings`**.
- Migrate: key đang có trong file → chuyển vào keychain, ghi
  `api_key: "keychain"` vào file.

**Accept**

- [ ] macOS: key vào Keychain, file không còn plaintext.
- [ ] Không có keychain → vẫn chạy, có cảnh báo.
- [ ] Config cũ tự migrate một lần.
- [ ] Không thêm cgo dependency.

---

## T23 — custom keybindings + custom columns

**Effort** M · **Deps** T07 · **Lane** D

**Goal** — parity với `views.yaml` của k9s: đổi phím, đổi cột hiển thị.

**Files** — `internal/config/views.go`, `internal/ui/model.go`,
`internal/k8s/kinds.go`, `docs/config.md`

**Design** — `~/.k10s/views.yaml` (chung file với T12, section khác):
cột theo kind, cột từ label (`labels.app`), cột từ JSONPath đơn giản. Keybinding
map `action → key`, phát hiện trùng lúc load và báo, không im lặng lấy cái cuối.

**Accept** — [ ] đổi cột Pods thêm `labels.app` chạy được · [ ] phím trùng báo
lỗi rõ · [ ] file hỏng không chặn startup.

---

## T24 — export CSV/JSON + clipboard OSC 52

**Effort** S · **Deps** T07, T08 · **Lane** B

**Goal** — `ctrl+e` xuất bảng đang xem, `Y` copy YAML vào clipboard.

**Design** — export tôn trọng filter + sort + marked (T08). Clipboard dùng
**OSC 52** để copy được cả khi chạy qua ssh/tmux; fallback ghi file + toast.

**Accept** — [ ] CSV mở được bằng spreadsheet · [ ] copy qua ssh vào được
clipboard máy local · [ ] terminal không hỗ trợ OSC 52 → fallback file.

---

## T25 — packaging: brew / scoop / nix

**Effort** M · **Deps** T21 · **Lane** D

**Design** — homebrew tap `p10node/tap`, scoop bucket, nix flake; workflow
release tự bump. Giữ `install.sh` làm đường mặc định.

**Accept** — [ ] `brew install p10node/tap/k10s` chạy · [ ] tag mới tự bump
formula · [ ] self-update biết mình cài bằng package manager thì bảo user dùng
package manager, không tự ghi đè.

---

## T26 — kinds còn thiếu

**Effort** S · **Deps** none · **Lane** A

IngressClass · VPA · PriorityClass · MutatingWebhookConfiguration ·
ValidatingWebhookConfiguration · Lease · VolumeSnapshot · CSIDriver.

Theo đúng 5 bước `docs/dev.md` § "Adding a resource kind" cho từng cái. Mỗi kind
một commit riêng để review được.

**Accept** — [ ] mỗi kind có row formatter + mock mirror + test · [ ]
`RowCount` không mở watch · [ ] sidebar count đúng.

---

# P4

Spec đầy đủ: [lenses.md](lenses.md) — đọc trước khi làm bất kỳ card nào ở đây.
Mọi lens dùng `dynamic` + `unstructured`. **KHÔNG thêm module Go nào.**

## T27 — lens schema + loader

**Effort** M · **Deps** none · **Lane** F

**Goal** — `internal/lens` parse file YAML theo schema ở `lenses.md`, validate,
và trả về `[]Pack`. Lens builtin nhúng bằng `go:embed`; lens người dùng ở
`~/.k10s/lenses/*.yaml`, trùng `name` thì đè.

**Files** — `internal/lens/lens.go`, `internal/lens/builtin/*.yaml`,
`internal/lens/lens_test.go`.

Validate phải bắt: `gvr` không đúng dạng `group/version/resource`, `verb` lạ,
`path` JSONPath không parse được, `action` id không tồn tại trong `actions[]`,
`severity` trỏ tới bảng chưa khai báo. Lỗi kèm tên file + tên pack; **một pack
hỏng không được làm chết pack khác** (cùng luật với `internal/plugin`).

**Accept** — [ ] pack hợp lệ round-trip · [ ] mỗi loại lỗi trên có một test
khẳng định message · [ ] pack hỏng bị bỏ qua, pack còn lại vẫn load · [ ] không
đụng `go.mod`.

---

## T28 — dynamic kind registry

**Effort** L · **Deps** T27 · **Lane** F

**Goal** — kind của lens xuất hiện trong Resources pane **chỉ khi** discovery
xác nhận mọi `group/version` trong `requires` đang được serve. Mỗi GVR một
dynamic informer, tạo lazy đúng như `Store.ensure` làm với kind builtin.

**Files** — `internal/k8s/lenskinds.go` (file mới), `internal/k8s/store.go`
(`Kinds()` merge; `gvrFor` nhận key của lens), `internal/mock/lens.go`.

Đây là card đổi kiến trúc: `Store.Kinds()` đang trả `Kinds()` tĩnh. Sau card
này nó là `builtin + lens đã qua discovery gate`. Discovery chạy **một lần** lúc
connect, kết quả cache; `SwitchContext` phải tính lại vì cluster khác có operator
khác.

**KHÔNG** đi qua `refreshCRs`. Đường đó list mọi CRD mỗi 15s; lens biết chính xác
GVR của nó nên dùng informer — ít API call hơn hiện tại, không phải nhiều hơn.

**Accept** — [ ] cluster không có operator → không kind nào của lens hiện ra, và
không có LIST nào được phát · [ ] fake discovery có `argoproj.io/v1alpha1` → kind
hiện ra · [ ] `RowCount` không mở watch (test gác `perf_test.go` vẫn xanh) ·
[ ] `SwitchContext` sang context không có operator thì kind biến mất.

---

## T29 — JSONPath columns + severity sort

**Effort** M · **Deps** T28 · **Lane** F

**Goal** — dựng row từ `columns[]`: JSONPath trên unstructured, `format`
(`age`/`bytes`/`int`/`bool`), `truncate`. `severity` map giá trị sang
`ok|warn|error|unknown`, dùng cho cả màu row lẫn comparator.

**Files** — `internal/k8s/lensrows.go`, `internal/ui/` (đường màu row),
`internal/lens/severity.go`.

Kind nào có cột severity thì **mặc định sort worst-first**. Đây là lý do tồn tại
của card: k9s #3589 (sort theo STATUS rải CrashLoopBackOff lẫn vào Running) bị
đóng *not planned*. Comparator, không phải feature.

`default:` trong bảng severity là bắt buộc cho CNPG — `.status.phase` của nó là
câu tiếng Anh (`"Cluster is unrecoverable and needs manual intervention"`), liệt
kê hết là thua.

Path trỏ vào field không tồn tại → ô rỗng, **không phải lỗi**. Nửa số status
field ở P4 là optional.

**Accept** — [ ] mỗi `format` một test · [ ] path thiếu → ô rỗng, không panic ·
[ ] severity sort đẩy `error` lên đầu, trong cùng bậc thì A→Z · [ ] `View` không
build row (perf guard).

---

## T30 — declarative actions

**Effort** L · **Deps** T29 · **Lane** F

**Goal** — 5 verb: `annotate`, `patch`, `status-patch`, `create`, `delete`.
Template var `.Name` `.Namespace` `.Context` `.Now` `.Selected`.

**Files** — `internal/k8s/lensactions.go`, `internal/ui/confirm.go`,
`internal/lens/template.go`.

`ack` là lý do đây là cơ chế chứ không phải năm cái nút: ghi annotation xong
watch field ack (`.status.lastHandledRefresh`), khớp thì tắt spinner. Ba trong
năm operator có field này.

`confirm: true` → modal **hiện câu `kubectl` tương đương**. `confirm: typed` →
phải gõ đúng tên object. Chỉ action failover/destructive mới dùng `typed`:
approval fatigue là failure mode có thật, prompt nhiều thì người ta bấm bừa.

`retryOnConflict` cho status-patch (CNPG promote chạy dưới optimistic lock).

**Accept** — [ ] mỗi verb một test với fake dynamic client · [ ] ack khớp thì
spinner tắt, không khớp thì vẫn quay · [ ] `typed` sai tên → không gửi request ·
[ ] modal chứa đúng câu kubectl · [ ] 403 hiện message nguyên văn của server.

---

## T31 — lens: argocd

**Effort** M · **Deps** T30 · **Lane** F

**Goal** — `argoproj.io/v1alpha1`: `applications`, `applicationsets`,
`appprojects`. Row = sync + health (đúng printer column upstream).

**Files** — `internal/lens/builtin/argocd.yaml`, `internal/mock/lens.go`.

Action: **sync** ghi `.operation` top-level (Application không có status
subresource, một `update` là đủ); **refresh** annotate
`argocd.argoproj.io/refresh: normal|hard`; **terminate** set
`.status.operationState.phase = Terminating`; **rollback** = sync với revision
lấy từ `.status.history[]`.

Từ chối sync khi `.operation != nil` và khi `.spec.syncPolicy.automated` bật.
**KHÔNG hardcode `-n argocd`** — apps-in-any-namespace GA từ 2.5, list toàn
cluster, key theo `<ns>/<name>`.

🔴 Modal sync phải nói thẳng: ghi `.operation` **đi vòng qua `argocd-rbac-cm`**.
Policy đó do `argocd-server` enforce; update thẳng lên CR thì Argo không thấy.

**Accept** — [ ] sync ghi đúng shape `.operation` · [ ] đang có operation →
từ chối, message `ErrAnotherOperationInProgress` · [ ] modal chứa cảnh báo RBAC ·
[ ] row đọc được app ở namespace bất kỳ · [ ] `resourceHealthSource: appTree` →
cột health per-resource để trống kèm ghi chú, không phải để trống câm.

---

## T32 — lens: cnpg

**Effort** M · **Deps** T30 · **Lane** F

**Goal** — `postgresql.cnpg.io/v1`: `clusters`, `backups`, `scheduledbackups`,
`poolers`. Row = `readyInstances/instances` + `currentPrimary` + phase.

**Files** — `internal/lens/builtin/cnpg.yaml`, `internal/mock/lens.go`.

Action một annotation hoặc một status patch: **fence**
(`cnpg.io/fencedInstances`, chuỗi JSON array — `'["pg-1"]'`, `"*"` = cả cluster),
**hibernate** (`cnpg.io/hibernation: on|off`), **restart**
(`kubectl.kubernetes.io/restartedAt`), **reload** (`cnpg.io/reloadedAt`),
**backup** (tạo `Backup` CR), **promote** (patch `clusters/status`,
conflict-retry).

Backup recency **không** lấy từ `.status.lastSuccessfulBackup` — cả họ field đó
deprecated vì backup chuyển sang plugin CNPG-I. Tính từ `Backup` CR
(`.status.stoppedAt`, `.status.phase`).

**KHÔNG** scrape port 8000: từ 1.30 endpoint nhạy cảm của instance-manager đòi
client cert ECDSA ghim sẵn của operator.

**Accept** — [ ] fence ghi đúng chuỗi JSON array · [ ] promote patch
`/status` và retry khi conflict · [ ] phase ngoài `"Cluster in healthy state"`
đều là warn · [ ] backup age lấy từ `Backup` CR.

---

## T33 — lens: longhorn

**Effort** L · **Deps** T30 · **Lane** F

**Goal** — `longhorn.io/v1beta2` (`v1beta1` đã bị xoá ở 1.10.0). Khai báo **bốn**
kind, không phải 25: `volumes`, `nodes`, `backups`, `snapshots`.

**Files** — `internal/lens/builtin/longhorn.yaml`, `internal/mock/lens.go`.

Row volume = `state` + `robustness` + `currentNodeID` +
`.status.kubernetesStatus.{pvcName,workloadsStatus[]}` — backref tới workload
đang thực sự dùng đĩa, thứ kubectl không cho.

Action khai báo được: snapshot (tạo `Snapshot` CR, `spec.createSnapshot: true`),
backup (tạo `Backup` CR — 🔴 **bắt buộc label `longhorn.io/backup-volume: <vol>`**,
controller select theo nó), attach/detach (patch `spec.attachmentTickets` trên
`VolumeAttachment` CR — CR này **trùng tên với volume**), replica count, node
scheduling.

**KHÔNG** làm: trim, salvage, snapshot-revert, engine upgrade. Chúng chỉ có trên
HTTP API `:9500`, mà `networkPolicies.restrictInternalTraffic` mặc định chặn ở
1.12 và API đó **không có auth riêng**. Mọi action trong lens thừa hưởng RBAC của
kubeconfig và vào audit log — đáng giá hơn bốn cái nút.

Quy mô là ràng buộc thật: 1 volume = 1 Volume + 1–2 Engine + N Replica + 1
VolumeAttachment + một Snapshot CR mỗi snapshot (kể cả snapshot hệ thống). 500
volume ⇒ 10k+ object. Informer, không poll-list. Select replica theo label
`longhornvolume: <name>`. Snapshot view mặc định lọc `userCreated: true`.
**KHÔNG hardcode `longhorn-system`** — discover qua DaemonSet.

**Accept** — [ ] backup CR có label bắt buộc · [ ] detach xoá đúng ticket của
mình, không đụng ticket csi-attacher · [ ] snapshot view mặc định chỉ
`userCreated` · [ ] namespace lấy từ discovery · [ ] test 500 volume không mở
LIST nào ngoài informer.

---

## T34 — lens: kargo

**Effort** M · **Deps** T30 · **Lane** F

**Goal** — `kargo.akuity.io/v1alpha1`: `stages`, `freights`, `promotions`,
`warehouses`.

**Files** — `internal/lens/builtin/kargo.yaml`, `internal/mock/lens.go`.

⚠️ `Stage.status.currentFreight` và `Stage.status.phase` **không tồn tại**. Dùng
`.status.freightSummary` (sinh ra để làm cột bảng) + `.status.health.status`.
`Freight` **không có `spec`** — `alias`, `origin`, `commits`, `images` nằm top-level.

Action: **promote** tạo `{generateName: promo-, spec:{stage, freight}}` rồi để
mutating webhook bơm `spec.steps` — **KHÔNG dựng steps phía client**. **approve
freight** là status patch trên `freights/status`, không phải annotation.
**refresh** / **abort** / **re-verify** là annotation.

🔴 Promote: validating webhook gửi SubjectAccessReview cho verb ảo **`promote`**
trên `stages`. `create` trên `promotions` **không đủ**. Lỗi về dưới dạng webhook
rejection lúc create — hiện nguyên văn message.

Tên Promotion là `<stage>.<ULID>.<hash>` → sort lexical = sort thời gian, miễn phí.
`Project` là cluster-scoped, reconcile ra namespace cùng tên có label
`kargo.akuity.io/project: "true"` — đó là project picker.

**Accept** — [ ] promote gửi đúng object tối thiểu, không có `spec.steps` ·
[ ] webhook reject hiện nguyên văn · [ ] approve dùng `freights/status` · [ ] cột
Stage đọc `freightSummary` · [ ] promotion sort theo tên ra đúng thứ tự thời gian.

---

## T35 — lens: traefik

**Effort** M · **Deps** T30 · **Lane** F

**Goal** — `traefik.io/v1alpha1` (group `traefik.containo.us` đã bị xoá ở v3):
`ingressroutes`, `middlewares`, `traefikservices`.

**Files** — `internal/lens/builtin/traefik.yaml`, `internal/k8s/traefikapi.go`.

🔴 **CRD của Traefik không có `.status`.** Không phải mỏng — không có. Không
condition, không event, không printer column; ClusterRole chỉ `get,list,watch`.
View dựng trên k8s API chỉ hiện lại được spec.

Tín hiệu có giá trị duy nhất — *IngressRoute của bạn `disabled` vì match rule
sai* — chỉ nằm ở HTTP API (`/api/http/routers`: `status` là
`enabled|disabled|warning`, `error[]` nói lý do). Trong Helm chart chính thức API
đó **không lộ ra ngoài pod** (`ingressRoute.dashboard.enabled: false`,
`expose.default: false`, không có Service port 8080).

Nên: mặc định spec-only. **Router inspector là opt-in**, port-forward API rồi
join row CRD với router sống theo quy ước tên `<ns>-<name>-<hash>@kubernetescrd`.
Không có API → degrade về spec-only, không phải báo lỗi.

Card này **không phải** headline feature và card này nói thẳng như vậy. Muốn view
ingress hạng nhất thì làm Gateway API (T37, xem `lenses.md`): nó có đúng thứ
Traefik CRD thiếu, và một lens phủ luôn Istio + Envoy Gateway + Cilium.

**Accept** — [ ] không có API → spec-only, không lỗi · [ ] có port-forward →
join đúng router, hiện `disabled` + `error[]` · [ ] join fail (hash lạ) → ô rỗng,
không đoán bừa.

---

## T36 — edges: điều hướng quan hệ

**Effort** L · **Deps** T31, T32, T33 · **Lane** F

**Goal** — `edges[]` trong lens: `via` là `label` | `ownerRef` | `annotation` |
`field`. Điều hướng hai chiều: từ pod đi **lên** CNPG Cluster, **ngang** sang
Backup mới nhất, **xuống** PVC, ngang tiếp sang Longhorn Volume, rồi tới node
đang giữ replica degraded.

**Files** — `internal/lens/edges.go`, `internal/ui/treeview.go`.

Bảng edge phải là **data**, không hardcode ownerRef — ownerRef không diễn đạt
được hop nào ở trên. Headlamp kết luận y hệt và cho plugin định nghĩa quan hệ
Map từ v0.45. k9s XRay chỉ đi xuống (deploy→rs→pod); đi lên và đi ngang là đất
chưa ai chiếm trong terminal UI.

Resolve edge **lazy**, chỉ khi user mở panel quan hệ. Chặn độ sâu render
(TraefikService tham chiếu TraefikService đệ quy được).

**Accept** — [ ] mỗi `via` một test · [ ] chuỗi pod→cluster→pvc→volume đi được
cả hai chiều · [ ] edge trỏ tới kind chưa load → hiện "chưa load", không tự mở
watch · [ ] cycle không treo UI.

---

# P5

Full spec: [../SPEC.md](../SPEC.md). The cards below are the dispatch-sized
version; where a card is silent, SPEC.md is the source of truth.

---

## T37 — frame memo: one `Rows()` per frame

**Effort** S · **Deps** none · **Lane** B · **Done**

**Goal** — `View()` calls `src.Rows()` exactly once.

**Why** — `tableData()` (`internal/ui/model.go:730`) has no memo. In table
mode one frame calls it **4–6 times**: `view.go:695`, `view.go:750`,
`view.go:976` (twice, via `curName()` → `curRow()` → `tableData()`,
`model.go:833`/`:841`), and `view.go:1023` twice more via `rowStatus`.
`BenchmarkRowsPods` is 488µs / 8018 allocs for 2000 pods
(`performance.md:52`). Every later P5 card adds work to this frame; without
this one first, none of them can show they did not slow it down.

**Files** — `internal/ui/model.go`, `internal/ui/view.go`,
`internal/ui/palette.go`

**Design**

- `rowsMemo` keyed on `(kind, namespace, search)`, cleared at the top of
  **both** `Update` and `View` — the pattern `kindsMemo` already uses, written
  down at `performance.md:125-130` ("never staler than one frame").
- `paletteHits()` has the same problem: once per frame at
  `palette_view.go:20`, and **twice per click** (`model.go:2747`, `:2750`).
- Do not change `tableData()`'s signature; add the cache behind it.

**Accept**

- [x] One `View()` → exactly one `Rows()`.
- [x] One palette click → exactly one `paletteHits()`.
- [x] `BenchmarkView` allocs/op does not rise.
- [x] `just shot 160 48` byte-identical to the frame before the change.

**Tests** — `TestViewBuildsRowsOnce`, counting through a source stub the way
`model_test.go:87` already does.

**Shipped note** — `RowCount` had to join the memo key. A caller can mutate
the cluster and re-read with no message in between, and a memo keyed only on
kind/namespace/search hands back the row it just deleted.

---

## T38 — perf guards that measure navigation

**Effort** S · **Deps** none · **Lane** B · **Done**

**Goal** — `TestKeypressLatency` measures the table, not the prompt.

**Why** — `model_test.go:97-121` drives `key("j")` and `key("k")`. In
`focusMain`, `j` is unbound and `k` calls `openPrompt("k")`
(`model.go:1666`). From the second iteration **every keystroke lands in the
text field**, so the frame being measured is a zoomed prompt with a ~400-char
buffer. `BenchmarkKeypressFrame` (`bench_test.go:36-47`) has the identical
defect. The guard does not guard what it claims to.

**Files** — `internal/ui/model_test.go`, `internal/ui/bench_test.go`

**Design**

- Drive `key("down")` / `key("up")`, which reach `m.move()`
  (`model.go:1660`).
- Tighten `model_test.go:87` from `gotRows >= nKinds` (30, roughly 7× the
  real number of 4) to `gotRows > 1`. After T37 the true answer is 1.
- Assert focus is still `focusMain` after the drive loop.

**Accept**

- [x] Red when T37 is reverted, green with it.
- [x] The latency test asserts focus did not move.
- [x] `just test-perf` green.

---

## T39 — layout budget by terminal width

**Effort** M · **Deps** none · **Lane** B · **Done**

**Goal** — 80×24 is usable: nothing clipped mid-token, no buttons lost.

**Why** — `headerH: 4` is hardcoded (`model.go:919`), and row 2 is blank while
row 4 is a rule (`view.go:188-192`). With a 3-row prompt and a 1-row status
bar that is **8 of 24 rows (33%)** of chrome before a single pod is drawn.
`leftW`/`rightW` are constants (`model.go:929-931`) that only collapse on `z`,
so the side panes take **38 of 80 columns (47%)**. Neither header line has a
width budget: the 80-column frame ends at `│  nodes`, and the `ns ▾` /
`theme ⟳` buttons sit off screen **with their zones still marked** — the mouse
affordance dies silently. Row 3 ends `42%    81`, cut inside the number.

**Files** — `internal/ui/model.go` (`layout()`), `internal/ui/view.go`
(`viewHeader`), `docs/ui.md`

**Design**

| Width | Header | Left | Right |
|---|---|---|---|
| ≥ 120 | 4 rows, gauge 16, absolute figures shown | 22 | 24 |
| 96–119 | 2 rows (no blank, no rule), gauge 10, figures hidden | 18 | 20 |
| < 96 | 1 row, gauge 6 | 18 | 0 |

- `headerH` becomes a function of `m.w`. It is already read through `l`
  everywhere (`view.go:193`, `palette_view.go:93`), so the change is
  contained.
- Each header segment is assembled against the remaining budget and **dropped
  whole, never cut**. A zone is marked only if its segment was drawn.
- Below 96, the Actions pane goes first and the sidebar stays: its keys are
  also on the status bar and in the palette, whereas the sidebar is the only
  thing saying where you are. `z` still collapses both.

**Accept**

- [x] `just shot 80 24` — no token cut mid-word; MEM shows in full or not at
      all.
- [x] `just shot 80 24` — the pod table gets ≥ 15 data rows.
- [x] No zone is scannable whose segment was not drawn.
- [x] `just shot 160 48` unchanged.

**Tests** — `TestHeaderNeverClipsMidToken` at 80/96/120,
`TestHeaderZoneIsMarkedOnlyWhenDrawn`, `TestHeaderFitsItsWidth`.

**Shipped note** — the accept bar originally said ≥ 18 data rows, which is
arithmetically impossible with a 3-row prompt and a 1-row status bar; the real
ceiling is 17 and the bar is now 15. Two further corrections during the work:
the demo tag is its own segment with a short form, because folded into the
context segment it outranked everything and then took the context name down
with it; and separators shrink to `" · "` below 120 columns, which buys back
the node counter.

---

## T40 — column policy: weight, priority, measured in cells

**Effort** M · **Deps** T37 · **Lane** B · **Done**

**Goal** — NAME is not truncated while a less important column is still on
screen.

**Why** — three defects, all in `fitCols`/`tryFit` (`view.go:373-436`):

1. The shrink loop takes from the **widest** column over its minimum
   (`view.go:422-428`), which is always NAME. Measured: at 100 columns NAME is
   already `api-gateway-7d9f4…` *while four columns are still displayed*, so
   two pods differing only in their hash suffix render identically.
2. `keep = keep[:len(keep)-1]` (`view.go:383`) drops right to left, with no
   notion of importance. Measured: AGE dies first at every width.
3. `tryFit` measures with `len(r[ci])` — **bytes** (`view.go:395`, `:401`) —
   while cells are padded and cut by display width (`view.go:886`). A
   non-ASCII value (an Event message, an i18n namespace) over-reserves and
   pushes real columns off the right-hand edge.

**Files** — `internal/ui/view.go`, `internal/ui/columns.go` (new)

**Design**

- One lookup keyed on the header name, beside the three that already exist
  (`view.go:410`, `view.go:767`, `trend.go:64`): a `weight` and a `priority`
  per header.
- Shrink: take from the largest `natural[i] / weight[i]`. NAME weight 3;
  STATUS, READY weight 2; everything else 1.
- Drop: take the lowest `priority`, not the rightmost column. Identity 100
  (never dropped); STATUS 90; AGE 80; READY 70; the rest 50.
- Measure with `lipgloss.Width`, not `len`.
- **Do not** turn `Cols []string` into a struct. That touches 30 literals in
  `internal/k8s/kinds.go`, every positional row builder, `applyNamespace`
  (`rows.go:62`) and all of `internal/mock` — a shared file, see the hard
  limits.
- **Do not** build drag-to-resize. The target is the 2-cell `gap`
  (`view.go:749`) and it collides with the drag-to-select workflow `ctrl+s`
  exists to enable (`keybindings.md:31-36`). Weighted shrink is ~6 lines, no
  state, no gesture, and fixes what people actually complain about.

**Accept**

- [x] At 100 columns NAME has no `…` while a lower-priority column is shown.
- [x] At 80 columns STATUS shows in full, not `x Crash…`.
- [x] AGE survives to 80 columns.
- [x] A CJK cell does not push other columns off screen.
- [x] `just shot` at 80/100/140/160: no line exceeds the width.

**Tests** — `TestNameSurvivesUntilColumnsAreExhausted` (table over
80/100/140/160), `TestWidthIsMeasuredInCellsNotBytes`,
`TestLowestPriorityColumnDropsFirst`, `TestColumnNeverNarrowerThanItsHeader`.

**Shipped note** — weight and priority alone changed nothing, because NAME's
flat minimum of 18 let `tryFit` *succeed* by crushing it, so the drop loop
never ran. The identity column now asks for its natural width capped at 30,
which makes the fit fail and drops a column nobody was reading. A column is
also never cut below its own header: `RESTAR…` names nothing, so a column that
cannot afford its header leaves instead.

---

## T41 — honest columns: retire the ambiguous `-`

**Effort** M · **Deps** none · **Lane** A · **Done**

**Goal** — an empty cell says why it is empty.

**Why** — `-` currently means **five** things: unknown, unset, defaulted, not
applicable, and pending. All five render `subtle` (`view.go:452`), so they
read as a settled value. This is the repo's own "honest columns" principle
being broken by its own tables.

**Files** — `internal/k8s/rows.go`, `internal/ui/view.go` (`cellLevel`),
`internal/mock/data.go`, `docs/ui.md`

**Design**

A four-word vocabulary: `<none>` (deliberately absent), `<cluster>`
(cluster-scoped), `n/a` (not applicable to this object), `pending` (expected,
not yet — graded `warn` so `cellLevel` glyphs it), plus a real value wherever
one is known. Retire `-` entirely.

| Column | Where | Today | Should be |
|---|---|---|---|
| CPU/MEM (pods) | `rows.go:355` | `-` with no metrics-server | `n/a` when the API never answered, `pending` when it has but this pod has no reading |
| MIN (hpa) | `rows.go:900` | `-` where the Kubernetes default is 1 | `1` |
| IMAGE (deploy) | `rows.go:409` | first container only, header unqualified | `<image> +2` |
| ADDRESS (ingress) | `rows.go:562` | `-` for both "just created" and "3-day outage" | `pending` |
| CAPACITY (pvc/pv) | `rows.go:598`, `:1233` | `-` means unbound (PVC) or a spec bug (PV) | distinguish; unbound reads `Status.Phase` |
| DURATION (job) | `rows.go:474` | `-` for a job that has not started | `pending` |
| NAMESPACE (cluster-scoped CR) | `rows.go:830` | `-` | `<cluster>` |

**Accept**

- [x] No bare `-` survives in any demo table.
- [x] With no metrics-server, CPU/MEM say `n/a` rather than a column of
      dashes.
- [x] `pending` is graded and carries a glyph, not colour alone.
- [x] `just shot` in both states.

**Tests** — `TestSentinelVocabularyIsGraded`,
`TestPendingCellsAreGlyphedInTheTable`, `TestNoBareDashesInTheDemoTables`.

**Shipped note** — two corrections. READY on a pod with init containers was
listed here as disagreeing with STATUS; it does not — counting only
`Spec.Containers` is what `kubectl` does, and changing it would diverge from
the tool operators check against. And metric columns spend their two reserved
cells on the trend arrow, so a graded sentinel there would have been the one
cell in the table carrying severity in colour alone; a graded value now takes
the glyph in those same cells.

---

## T42 — row groups: group by owner, on by default for Pods

**Effort** L · **Deps** T37, T40, **B:T07** · **Lane** B · **Done**

**Goal** — opening Pods shows each pod under its owner, with sort, filter, row
numbering and selection behaving exactly as they did.

**Why** — see [../SPEC.md](../SPEC.md) §3 M6. In short: a real tree in the
main table breaks five things that work today (sort becomes undefined, filter
forks into two wrong answers, row numbers stop being addressable, a 500-pod
namespace gets worse, selection doubles in arity). **One level** of grouping
gives the same read and loses none of them. The multi-hop tree stays in the
`X` panel (`tree.go:62`) — that is card **T14**, and this one does not
duplicate it.

**Files** — `internal/ui/rowgroups.go` (new), `internal/ui/view.go`,
`internal/ui/model.go`, `internal/k8s/rows.go` (OWNER cell),
`internal/config/config.go`, `docs/ui.md`, `docs/config.md`

**Design**

- **The owner needs no new watch.** `metadata.ownerReferences` is **already on
  the pod**. Group by `ownerReferences[0].name`: no extra request, no extra
  informer (`performance.md:31`, `TestOpeningOneKindWatchesOnlyThatKind` stays
  green).
- The Deployment name is **derived**, not fetched: the ReplicaSet
  `web-frontend-6b8c7d9f5` matches a pod-template-hash suffix and yields
  `web-frontend`, drawn as `web-frontend · rs 6b8c7d9f5`. When the shape does
  not match, print the owner name as-is. **Never print a Deployment name that
  was not derived from a matched pattern.**
- The owner rides as a meta cell past `len(Cols)`, not as a column: grouping
  must read a value already in the row, but a visible OWNER column would put a
  cell nobody asked for on every pod table.
- Group keys: `owner` (default for Pods), `node`, `namespace` (only under
  `:ns all`), `status`, `object` (default for Events), `none`. No nesting.
  `label:<k>` is **absent** — labels are not in the row set, and a key that
  returns one group called `<none>` for everything is worse than no key.
- Behaviour mirrors the sidebar, whose rules are already tested
  (`groups_test.go`) and written down at `ui.md:83-85`:
  - `map[groupKey]map[value]bool`, **all open** by default, **not persisted** —
    a folded row group saves no requests (unlike the sidebar), so reopening a
    session with half the pods hidden is a surprise.
  - `space` folds the group under the cursor, and stays a search character
    while searching (`model.go:1481`). No `left` binding — `←` focuses the
    sidebar.
  - **A search ignores folding entirely.** Verbatim `model.go:697-699`. A
    group with no matches disappears rather than showing an empty header.
  - **Sort and grouping are exclusive.** Sorting any column drops to flat and
    the panel title says so. This is what keeps T07 honest, and it is one
    branch instead of five.
  - Headers are unnumbered. Object rows keep **one continuous 1..N sequence
    across the table**, taken from the index in the ungrouped slice rather
    than the render index, so folding does not renumber the rows below. This
    is the only place `view.go:870` really changes.
  - Headers are not selectable; `↑`/`↓` skip them. Folding the group holding
    the selection moves it to that group's first row and marks the header
    (`groups_test.go:217`). `curRow()`, `curName()` and the Actions pane are
    **untouched**.
- Falls back to flat, silently, when: the kind has no such column; fewer than
  2 distinct values; more than 40 groups; more than 2000 rows.
- Cost: one O(N) pass over rows already in hand, one string compare per row,
  one `[]groupSpan`. Memoised beside `kindsMemo`, cleared at the top of
  `Update` and `View`. Collapse state is **not** in the memo — it is read at
  render time, so folding is a pure repaint.
- Config: `group: "pods=owner,events=object"` — one flat line, the shape
  `config.md:26` uses. Change **both** `render()` and `parse()`.

**Accept**

- [x] Opening Pods shows pods under owner headers, proven by `just shot 160 48`.
- [x] Sorting any column drops to flat, and the title says so.
- [x] Folding a group does **not** renumber the rows below it.
- [x] `f` plus a term still shows matches inside a folded group.
- [x] `↓` walks the whole table and `curRow()` never returns a header.
- [x] A 2000-pod namespace falls back to flat.
- [x] `TestOpeningOneKindWatchesOnlyThatKind` still green.
- [x] Empty `group` renders the frame this card replaced.

**Tests** — `TestGroupNoneRendersAFlatTable` (the regression gate for all of
P5); mirrors of the four sidebar rules (`groups_test.go:125`, `:217`, `:176`,
`:236`); `TestRowNumbersAreContinuousAcrossGroups`;
`TestCollapsingAGroupDoesNotRenumberRowsBelowIt`; `TestSortingDropsToFlat`;
`TestGroupingBuildsNoExtraRows`;
`TestPodsGroupByOwnerByDefault` (which also asserts no ReplicaSet is invented
for a name with no template hash).

**Shipped note** — `groupColumn` has to read the columns the backend actually
returned, not `Kind.Cols`. Under `:ns all` the prepended NAMESPACE column both
adds a key to group by and shifts every meta cell one to the right.

---

## T43 — view engine — CLOSED, NOT EXTRACTED

**Decided after building T42, T44 and T46.** This card was sequenced last so
the call could be made on evidence, and the evidence says: do not extract.

The card's argument was "adding a view should be a function and a case, not
another branch in `tableBody`". Two real views have landed since it was
written — the owner tree (T46) and the chart panel (T44) — and **neither
touched `tableBody`**: the tree has its own `treeBody` plus one branch in
`viewMain`, and the chart appends to the body. `viewMain` is already a flat
dispatch of early returns, each about eight lines.

Extracting now is churn and regression risk in exchange for nothing a user can
see. Reopen this card when a third view genuinely does not fit — not before.

The two fixes it carried are real and are **done**:

- [x] The table shows `n/m` on its title. The text view has had a position
      indicator since it was written (`38/66  57%`); the table never did, so
      scrolling a long namespace gave no sense of depth.
- [x] `←` / `h` — documented as "focus resource list" since the first release
      and **bound nowhere**; `h` fell through the Actions loop, then plugins,
      and did nothing. Both bound now. `l` is **not** bound (it is Logs) — the
      pair reads as symmetrical and is not, which is why the docs spell it
      `←` `h` / `→`.

---

## T43 (original) — view engine

Kept for the record; superseded by the decision above.

**Effort** M · **Deps** T39, T42 · **Lane** E

**Goal** — adding a view is a function and a case, not another branch in
`tableBody`.

**Why** — the centre pane is one hardcoded table. Four views are queued
(grouped, chart, port-forward manager, pulse) and each would grow another
branch inside a 1419-line `view.go` and a 2946-line `model.go`. Extract
**after** T42 proves a second mode exists, not before.

**Files** — `internal/ui/viewmode.go` (new), `internal/ui/view.go`

**Design**

```
rows   [][]string      // unchanged: flat, globally ordered
meta   []rowMeta       // parallel: groupValue, hidden, ordinal
spans  []groupSpan     // value, firstRowIdx, count
```

Modes: `table`, `grouped`, `text` (describe/YAML/help/tree), `chart`. Each is
a function from that contract plus a `layout` to a `Block`. `zoom`, the scroll
model and the zone namespaces are shared.

**Accept**

- [ ] `tableBody` knows nothing about grouping.
- [ ] Every `just shot` frame byte-identical to before the extraction.

---

## T44 — metric history + bar / sparkline / chart

**Effort** L · **Deps** T37 · **Lane** E · **Done**

**Goal** — see the shape of a number, not only its current value.

**Why, and the full design** — [../SPEC.md](../SPEC.md) §3 M8. Read it before
writing a line: it fixes every glyph, every colour token, the ASCII fallback,
and the reason for **not** adding a dependency.

**Files** — `internal/ui/gauge.go`, `chart.go`, `history.go` (all new),
`internal/ui/view.go`, `internal/ui/model.go`

**Design — the parts that must not drift**

- **Build it, ~135 lines.** `bubbles/progress` (already in go.mod) renders a
  gradient through a `lipgloss.Style` per segment — exactly the cost `paint`
  (`block.go:35`) exists to avoid, measured at 43% of the frame
  (`performance.md:106-120`). `ntcharts` brings its own canvas, viewport and
  zone handling, a second rendering model beside `block.go` and `zones.go` —
  and `zones.go:12-19` exists **because** bubblezone was removed on
  measurement.
- **Twelve colour tokens is a hard ceiling.** `theme.Theme` has exactly twelve
  (`theme/theme.go:16-30`) and custom themes are `UnmarshalStrict` with every
  field required (`theme.go:126-137`) — adding a token **breaks every existing
  user theme file**.
- **Bar** (`view.go:68-84` → `gauge.go`): block-eighths on a dotted trough.
  Fixes a real bug on the way: `filled := pct * width / 100` truncates with no
  floor (`view.go:76`), so at width 16 **every pct from 1 to 6 draws zero
  cells** — a node at 6% is identical to one at 0%. A negative `pct` is
  unguarded and panics in `strings.Repeat`. Hoist the hardcoded 60/85
  (`view.go:69-75`) to named constants; several call sites want them.
- **Sparkline** `▁▂▃▄▅▆▇█`, oldest to newest, scaled to **that row's own**
  maximum. Behind a toggle, off by default — the CPU column has no eight
  spare cells at 80 columns.
- **Chart**, braille (`U+2800` + bitmask), for one selected object. Below
  24×3 it does not draw: the bar and the number already say the current value.
- **History must not be fed from `View`.** `arrowFor` does exactly that today
  (`view.go:806-823`) and **must not be copied**: `View` runs per keystroke
  rather than per tick, and walks only visible rows (`view.go:850`), so the
  ring would both duplicate and hole. Feed it from the repaint tick in
  `Update`.
- Ring `[N]int32`, not `uint16`: a 64-core pod is 64000 milli, and MiB
  overflows 16 bits at 64 GiB.
- Sweep on the same tick: drop keys no longer in the row set — which also
  fixes the unbounded `m.trends` map (`trend.go:97-108`).
- **ASCII fallback** resolved **once at startup** from `K10S_ASCII=1`, a
  `$LANG` without `UTF-8`, or config. Never per call.

**Accept**

- [x] A node at 1% and a node at 0% draw differently.
- [x] 99% and 100% draw differently.
- [x] A negative `pct` or a zero `width` does not panic.
- [x] Two different grades never produce the same rune sequence.
- [x] Rendering 20 frames with no tick leaves the sample count unchanged.
- [x] `just shot 80 24` with `K10S_ASCII=1` is readable.
- [x] `BenchmarkView` allocs/op does not rise.

**Tests** — `TestGaugeShowsAnyUsageAtAll` (the 1–6% case, **wrong before this
card**); `TestGaugeDoesNotRoundUpToFull`; `TestGaugeWidthIsExact` for every
pct 0..100 at widths {6,10,16} in both glyph sets;
`TestGlyphSetsAreSingleWidth`; `TestHistoryIsNotFedByView`;
`TestHistorySweepDropsVanishedRows`; `TestChartPlotsTheShape`.

**Shipped note** — two departures from the plan above. The window is one store
of 64 samples, not 16 for the sparkline plus 120 for the chart: the sparkline
draws the tail of the same window, so there is no second sampling path to keep
in step. And the chart plots CPU alone rather than CPU and MEM together — a
second series needs a second stroke style to stay readable without colour, and
one series answered the question. The state strip (`▪ ▫ ▮ ▯`) was not built;
nothing asked for it yet.

---

## T45 — action search + typed gate

**Effort** M · **Deps** T37 · **Lane** D · **Done**

**Goal** — typing the name of a thing reaches that thing; and the two keys
that cannot be undone are not one `enter` away.

**Why** — the palette (`palette.go:54-92`) finds kinds and objects but **not
verbs**. `R`, `X` and `ctrl+y` appear in no pane and no hint string
(`view.go:1172`). And `D` delete sets `danger: true` but gates only on `enter`
(`model.go:2181`) — `enter` is also the universal "open" key, so `D`,`enter`
deletes. `u` drain (`model.go:2229`) is the same, and drain is the heaviest
key in the app. The typed gate **already exists** and lens packs already use
it (`lens.go:139-140`).

**Files** — `internal/ui/palette.go`, `internal/ui/model.go`

**Design**

- `paletteHits` also matches `Actions`, lens specs and plugins; the `sub` line
  names the kinds it applies to; firing goes through `fireAction`. Reuses the
  palette's overlay, key handling, zones and mouse path — no new modal, no new
  focus state.
- `typed: name` on `D` (`model.go:2183`) and `u` (`model.go:2232`).
- `e` → apply (`model.go:1189-1210`) applies **unconditionally** when the
  editor exits: `:q` out of `vi`, a file truncated by a crashed editor, an
  empty file — all reach `src.Apply`. Compare against the fetched bytes; skip
  silently when identical.
- `o` cordon stays ungated — it is a toggle and the label flips.
- Preserve the invariant: `confirm.armed()` gates identically on the keyboard
  (`model.go:1318`) and on the mouse OK button (`model.go:2729`).

**Accept**

- [x] Typing "describe" in the palette reaches Describe.
- [x] `R`, `X` and `ctrl+y` are findable by name.
- [x] `D`, `enter` does **not** delete.
- [x] `e` then `:q` writes nothing to the cluster.
- [x] The mouse OK button and `enter` gate identically.

**Tests** — `TestDeleteRequiresTypedName`; `TestDrainRequiresTypedName`;
`TestEditWithNoChangesDoesNotApply`; `TestEditWithAnEmptyFileDoesNotApply`;
`TestEditWithChangesStillApplies`; `TestPaletteFindsActionsByName`.

**Shipped note** — `r` restart was listed here for a `danger` flag when
replicas < 2 and was not done: the replica count is not on the action path
without another read, and the rollout is reversible. `handleMouse` was also
calling `paletteHits()` twice per click on top of the once-per-frame the
overlay costs; asked once now.

---

## T46 — nested owner tree in the main table

**Effort** L · **Deps** T42 · **Lane** B · **Done**

**Goal** — `t` opens a multi-level tree in the main table: Deployment →
ReplicaSet → Pod, indented, every node selectable.

**Why** — T42 deliberately groups **one level** so it does not break sort,
filter, row numbering or selection ([../SPEC.md](../SPEC.md) §3 M6 lists all
five). This card does the thing T42 refused, but **opt-in and afterwards**,
once T42 has been seen on a real frame and a tree is still wanted. That order
is the point of the card: doing it first would build the expensive version
before knowing whether the cheap one was enough.

**Files** — `internal/ui/treeview.go` (new), `internal/ui/model.go`,
`internal/ui/view.go`, `docs/ui.md`, `docs/keybindings.md`

**Design**

The five things T42 avoids, each of which this card has to answer — and if it
cannot, stop and say so rather than guess:

1. **Sort.** A tree orders siblings, so a global sort means nothing inside
   one. Sorting **closes the tree**, the same rule T42 uses for grouping:
   sort and structure are exclusive.
2. **Filter.** Show matches **with their ancestors**, and draw the
   structure-only ancestors `subtle` and unselectable. A row that is on screen
   without matching has to be obviously not a result, or the filter looks
   broken.
3. **Row numbers.** In tree mode, **drop the number column** and use the
   branch drawing (`├─`, `└─`, `│`). A number exists to address a row; in a
   tree it addresses nothing stable, so keeping it is a lie. `rowNumBase`
   (`view.go:1377`) is untouched — table mode is unchanged.
4. **Scale.** A hard ceiling, as `treeMaxNodes` (`tree.go:43`) already does
   for the `X` panel: past **300 nodes**, do **not** open, and say the number
   in a toast. A tree that silently truncates is indistinguishable from a
   cluster that really is that small.
5. **Selection.** Parent nodes are real objects, selectable, with the Actions
   pane following the selected node's kind — unlike T42, where a header is not
   an object. This is the expensive part: `curRow()` (`model.go:820`) returns
   the row of **one** kind, and a tree mixes kinds in one list.

**The watch this needs is the hardest constraint.** `pod.ownerReferences`
gives the ReplicaSet name for free (T42 uses exactly that), but **ReplicaSet →
Deployment needs the ReplicaSet object**, which means a ReplicaSet informer.
Opening Pods deliberately starts no other informer (`performance.md:31`,
`TestOpeningOneKindWatchesOnlyThatKind`).

The rule: opening the tree is an **on-demand** user action, so it may start
the missing informers — but **only on the keypress**, never on opening the
kind, and the toast must say which watches it started.

**Accept**

- [x] `t` on Pods gives Deployment → ReplicaSet → Pod at the right depths.
- [x] Filtering shows ancestors `subtle` and unselectable.
- [x] Tree mode has no number column and draws `├─ └─ │`.
- [x] Over 300 nodes does not open, and the toast says the number.
- [x] Selecting a Deployment node shows Deployment actions.
- [x] Opening Pods **without** `t` keeps
      `TestOpeningOneKindWatchesOnlyThatKind` green.
- [x] `TestKeypressLatency` stays green.
- [x] `just shot 140 34 t`.

**Tests** — `TestTreeNestsPodsUnderReplicaSetsUnderDeployments`;
`TestTreeActionsFollowTheSelectedNodesKind`;
`TestTreeDoesNotRepointTheUnderlyingTable`;
`TestTreeFilterKeepsAncestorsUnselectable`; `TestTreeCursorSkipsAncestors`;
`TestTreeGlyphsCloseTheirBranches`; `TestTreeKeepsPodsWithNoDeployment`.

**Shipped note** — three departures. The key is `t`, not `T`: `T` already
cycles the theme. The kind override is a new `targetKind()`, deliberately not
folded into `curKind()` — `tableData` keys on `curKind`, so overriding it
would repoint the whole table at the cursor; there is a test for that. And it
is not a mode of a view engine, because T43 was closed without building one.

---

## Prompt template

Dán nguyên khối này cho worker agent, thay `<ID>`:

```
Bạn làm việc trên repo k10s (Kubernetes TUI, Go, bubbletea) tại thư mục hiện tại.

Nhiệm vụ: hoàn thành đúng task <ID> trong docs/plan.md.

Bắt buộc, theo thứ tự:

1. Đọc docs/plan.md — TOÀN BỘ section "Worker contract" và card <ID>.
   Worker contract là các invariant của repo; phá là CI đỏ hoặc UI vỡ âm thầm.
2. Đọc các file trong mục "Files" của card, cộng docs/architecture.md.
   Nếu card đụng internal/k8s hoặc render path, đọc thêm docs/performance.md.
3. Implement đúng phần "Design". Chỉ sửa file trong "Files" của card.
   Thấy cần file khác → làm xong rồi ghi vào báo cáo, đừng tự mở rộng scope.
4. Viết test cho từng gạch đầu dòng trong "Accept". Test backend dùng fake
   clientset qua newTestStore; nhớ syncKinds(t, s, kPods, …) trước khi assert
   rows.
5. Chạy `just check` (fmt-check + vet + test). Phải xanh.
6. Feature có thay đổi UI → chạy `just shot 140 44 "<keys>"` và dán frame vào
   báo cáo.
7. Cập nhật docs theo "Definition of done" mục 4.
8. Tick card <ID> trong docs/plan.md § Board.

Giới hạn cứng:
- KHÔNG thêm method vào domain.Source. Capability mới đi bằng optional
  interface + type-assert, cả internal/k8s và internal/mock đều impl.
- KHÔNG I/O trong render path. KHÔNG watch mới lúc startup.
- KHÔNG thêm dependency mới trừ khi card nói rõ.
- KHÔNG commit, KHÔNG push, KHÔNG tag. Để diff ở working tree.
- KHÔNG refactor ngoài phạm vi card.

Báo cáo cuối, đúng 5 mục:
- Đã làm gì (theo từng gạch đầu dòng Accept)
- File đã sửa + vì sao
- Test đã thêm + output `just check`
- Frame `just shot` nếu có đổi UI
- Điều gì lệch khỏi card, và lý do
```

## Lane prompt template

Cho lane mode. Thay `<LANE>`, `<TÊN LANE>`, `<VÙNG SỞ HỮU>`, `<DANH SÁCH CARD>`,
`<DEPS CẮT NGANG>`:

```
Bạn là worker agent của lane <LANE> (<TÊN LANE>) trên repo k10s
(Kubernetes TUI, Go, bubbletea) tại thư mục hiện tại.

Lane của bạn sở hữu: <VÙNG SỞ HỮU>
Bốn lane khác đang chạy song song trên vùng file khác. Ra khỏi vùng của mình
là gây conflict cho người khác.

SETUP (làm một lần):
  git checkout -b lane/<LANE>-<tên> main

Đọc trước khi sửa bất cứ gì:
  - docs/plan.md § "Worker contract" — TOÀN BỘ. Đây là invariant của repo.
  - docs/plan.md § "Dispatch protocol" — luật chống conflict giữa 5 lane.
  - docs/architecture.md

CARD, làm TUẦN TỰ đúng thứ tự này:
<DANH SÁCH CARD>

Với MỖI card, lặp lại đủ vòng:
  1. git rebase main   (lane khác có thể đã merge thứ bạn cần)
  2. Đọc card trong docs/plan.md: Goal, Why, Files, Design, Accept, Tests, Not.
     Card đụng internal/k8s hoặc render path → đọc thêm docs/performance.md.
  3. Implement đúng Design. Chỉ sửa file trong "Files" của card.
  4. Viết test cho TỪNG gạch đầu dòng trong "Accept". Test backend dùng fake
     clientset qua newTestStore; nhớ syncKinds(t, s, kPods, …) trước khi assert
     rows.
  5. `just check` phải xanh.
  6. Có đổi UI → `just shot 140 44 "<keys>"`, giữ frame cho báo cáo.
  7. Cập nhật docs theo Definition of done mục 4, tick card ở § Board.
  8. git commit -m "<ID>: <tiêu đề card>"   — KHÔNG push, KHÔNG tag.
  9. Sang card kế tiếp.

DEPS CẮT NGANG LANE:
<DEPS CẮT NGANG>
  Dep chưa có trên main → BỎ QUA card đó, làm card kế tiếp, ghi "hoãn" vào báo
  cáo. TUYỆT ĐỐI không tự implement card của lane khác.

GIỚI HẠN CỨNG:
  - KHÔNG thêm method vào domain.Source. Capability mới = optional interface
    + type-assert; cả internal/k8s và internal/mock đều impl.
  - File dùng chung (domain.go, model.go, actions.go, internal/mock/) CHỈ ĐƯỢC
    THÊM, append cuối, một block một card, kèm comment card ID. Không sắp xếp
    lại, không đổi thứ tự field cũ, không sửa row mock đã có.
  - KHÔNG I/O trong render path. KHÔNG watch mới lúc startup.
  - KHÔNG thêm dependency, KHÔNG go mod tidy, KHÔNG đổi go.mod.
  - KHÔNG push, KHÔNG tag, KHÔNG merge vào main.
  - KHÔNG refactor ngoài phạm vi card.
  - Đụng file ngoài vùng sở hữu của lane → DỪNG, báo dispatcher, đừng tự quyết.

BÁO CÁO CUỐI (sau khi hết card), mỗi card một mục:
  - Card ID + xong / hoãn (lý do)
  - Từng gạch Accept: đạt hay không
  - File đã sửa
  - Test đã thêm + output `just check`
  - Frame `just shot` nếu có đổi UI
  - Điều gì lệch khỏi card, và lý do
  Cuối báo cáo: tên branch + `git log --oneline main..HEAD`.
```
