import "./style.css";
import * as App from "../wailsjs/go/main/App";
import { BrowserOpenURL, EventsOn } from "../wailsjs/runtime/runtime";
import type {
  Bootstrap,
  CudaTarget,
  DepsReport,
  LogLine,
  Model,
  Progress,
  SeparateResult,
  Settings,
  Track,
  UpdateStatus,
} from "./types";

// The generated bindings are untyped JS; funnel every call through here so the
// casts live in one place.
const api = App as unknown as Record<string, (...args: any[]) => Promise<any>>;

// ---------------------------------------------------------------- utilities

const $ = <T extends HTMLElement = HTMLElement>(id: string): T => {
  const el = document.getElementById(id);
  if (!el) throw new Error(`missing element #${id}`);
  return el as T;
};

const bytes = (n: number): string => {
  if (!n) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`;
};

const clock = (seconds: number): string => {
  if (!seconds || seconds <= 0) return "—";
  const s = Math.round(seconds);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  return h > 0 ? `${h}:${pad(m)}:${pad(sec)}` : `${m}:${pad(sec)}`;
};

const errText = (e: unknown): string => {
  if (typeof e === "string") return e;
  if (e instanceof Error) return e.message;
  return String(e);
};

function toast(message: string, kind: "info" | "error" | "good" = "info") {
  const el = document.createElement("div");
  el.className = `toast${kind === "info" ? "" : ` is-${kind}`}`;
  el.textContent = message;
  $("toasts").appendChild(el);
  window.setTimeout(() => el.remove(), kind === "error" ? 9000 : 4500);
}

// ------------------------------------------------------------------- state

interface State {
  settings: Settings | null;
  deps: DepsReport | null;
  models: Model[];
  track: Track | null;
  inputPath: string;
  busy: boolean;
}

const state: State = {
  settings: null,
  deps: null,
  models: [],
  track: null,
  inputPath: "",
  busy: false,
};

// ------------------------------------------------------------- cuda targets

// Filled once at startup from App.CudaTargets(). Kept module-level because the
// support panel has to re-render on every change of the CUDA dropdown.
let cudaTargets: CudaTarget[] = [];

// The capability the current table was built for, so renderDeps can tell "not
// asked yet" from "asked, and nothing fits". Keying the re-fetch off
// `!some(supported)` instead did both jobs badly: it never fired for a card
// that IS supported, so the ✓ verdict never appeared, and it fired on every
// deps event for a card no index covers.
let cudaTargetsCapability: string | null = null;

// False on macOS, where there is no CUDA index to choose. Kept separate from
// `cudaTargets.length` so an empty list means "not loaded yet" everywhere else
// and never permanently disables the install button.
let cudaPickerApplies = true;

// Set when CudaTargets() has failed. The list is a convenience — the stored tag
// is already a valid index, normalised by the settings store — so a failed
// fetch must not be allowed to hold the install button hostage forever.
let cudaTargetsUnavailable = false;

// The tag an install should use: whatever the user picked, or the stored one
// when the select has no options to pick from. That happens for a real stretch
// of every launch — the list is fetched asynchronously — and on any launch
// where the fetch fails outright. Reading the select raw sent cudaTag: "" in
// those windows, which the backend resolves to its own default rather than to
// the index the user chose last time.
// `?? ""` only covers the window before boot() has any settings, where nothing
// is installable yet anyway; once it does, the store has already normalised the
// tag to one of the offered indexes.
function currentCudaTag(): string {
  return $<HTMLSelectElement>("cuda-tag").value || (state.settings?.cudaTag ?? "");
}

// Bumped per call so a slow fetch that has been overtaken cannot write its
// stale list — and stale selection — over a newer one. renderDeps fires this on
// every capability change, so two can genuinely be in flight at once.
let cudaTargetsGeneration = 0;

async function renderCudaTargets(preferred: string, capability: string) {
  const generation = ++cudaTargetsGeneration;
  // Cleared per attempt, not only on success: left set from a previous failure
  // it would keep the install button unblocked for the whole of this fetch,
  // defeating the very wait the button exists for.
  cudaTargetsUnavailable = false;
  const select = $<HTMLSelectElement>("cuda-tag");
  let fetched: CudaTarget[];
  try {
    fetched = (await api.CudaTargets()) as CudaTarget[];
  } catch (e) {
    if (generation !== cudaTargetsGeneration) return;
    // index.html ships this select empty, so there is no earlier list to fall
    // back on and the user cannot pick an index at all. Say so — but do not
    // leave the install blocked: currentCudaTag() falls back to the stored tag,
    // which the settings store guarantees is one of the offered indexes.
    //
    // cudaTargetsCapability is deliberately NOT claimed here. renderDeps only
    // re-fetches when the capability differs from what the table was built for,
    // so marking a capability handled on the failure path meant one transient
    // error killed every retry for it — including the manual recheck button,
    // which re-reports the same cached capability.
    cudaTargetsUnavailable = true;
    refreshInstallEnginesButton();
    renderCudaSupport();
    toast(
      `Không lấy được danh sách bản CUDA: ${errText(e)}. Sẽ dùng bản đã lưu (${currentCudaTag()}).`,
      "error",
    );
    return;
  }
  if (generation !== cudaTargetsGeneration) return;
  // `?? []` belts the Go side's braces: a nil slice would arrive as null and
  // the loop below would throw, aborting boot() before autosave is wired.
  cudaTargets = fetched ?? [];
  cudaTargetsCapability = capability;

  select.textContent = "";
  for (const t of cudaTargets) {
    const opt = document.createElement("option");
    opt.value = t.tag;
    const notes = [t.recommended ? "mặc định" : "", `torch ${t.torch}`, `driver ${t.driver}`]
      .filter(Boolean)
      .join(" · ");
    opt.textContent = `${t.tag} (${notes})`;
    select.appendChild(opt);
  }
  // A saved choice wins; otherwise start on the recommended index.
  const fallback = cudaTargets.find((t) => t.recommended)?.tag ?? "";
  select.value = cudaTargets.some((t) => t.tag === preferred) ? preferred : fallback;
  refreshInstallEnginesButton();
  renderCudaSupport();
}

// Keeps only the accelerator and device options this machine can actually use.
// Offering the rest would let a user pick a backend their torch has no support
// for, which the app then silently downgrades to CPU.
//
// The allowed values arrive from Bootstrap rather than being re-derived from
// the platform string here. They come out of settings.AccelApplies, the same
// rule that normalises a settings file copied off another machine and that the
// installer refuses by, so this can no longer drift from either — the drift is
// what put MPS in the dropdown on Linux, where choosing it replaced a working
// CUDA torch with the CPU wheel.
function applyPlatformToInstallPanel(accels: string[], devices: string[]) {
  for (const [id, allowed] of [
    ["accel-select", accels],
    ["device-select", devices],
  ] as Array<[string, string[]]>) {
    for (const opt of Array.from(
      $<HTMLSelectElement>(id).querySelectorAll<HTMLOptionElement>("option"),
    )) {
      if (allowed.includes(opt.value)) continue;
      // Removed from the DOM, not just marked hidden: WebKit does not reliably
      // honour `hidden` on an <option>, and this app runs on WebKitGTK. The MPS
      // option stayed visible on Linux because of exactly that.
      opt.remove();
    }
  }

  // A CUDA index is only worth choosing where a CUDA wheel can be installed —
  // on macOS CudaTargets() returns nothing, and the install button must stop
  // waiting for a list that will never arrive.
  cudaPickerApplies = accels.includes("cuda");
  // classList, not the `hidden` property: #cuda-field carries .field, whose
  // `display: flex` is an author rule and therefore beats the user agent's
  // `[hidden] { display: none }`. Setting .hidden here did nothing at all and
  // left an empty CUDA dropdown on screen. `.hidden` in this stylesheet is
  // `display: none !important`, which is what the rest of the file uses.
  $("cuda-field").classList.toggle("hidden", !cudaPickerApplies);
  if (!cudaPickerApplies) $("cuda-support").classList.add("hidden");
  refreshInstallEnginesButton();
}

// The button waits for the options so the user gets to see which index they are
// installing — but only while the list is still on its way. Once the fetch has
// failed there is nothing left to wait for, and blocking on it would mean one
// unreachable call permanently disables installing; currentCudaTag() falls back
// to the stored index, which is a valid one.
function refreshInstallEnginesButton() {
  const ready =
    !cudaPickerApplies ||
    cudaTargetsUnavailable ||
    (cudaTargets.length > 0 && currentCudaTag() !== "");
  const btn = $<HTMLButtonElement>("btn-install-engines");
  btn.disabled = !ready;
  btn.title = ready ? "" : "Đang lấy danh sách bản CUDA…";
}

function renderCudaSupport() {
  const box = $("cuda-support");
  const tag = $<HTMLSelectElement>("cuda-tag").value;
  const target = cudaTargets.find((t) => t.tag === tag);

  // A failed fetch still leaves the install button enabled, so silence here
  // would be a claim: the panel would look exactly like a card that passed its
  // compatibility check. Say the check could not run instead — the user is
  // about to download several gigabytes on the strength of it.
  if (!target && cudaTargetsUnavailable && cudaPickerApplies) {
    box.classList.remove("hidden", "is-good");
    box.classList.add("is-warn");
    box.textContent =
      `⚠ Không kiểm tra được card có chạy được ${currentCudaTag()} hay không ` +
      `(chưa lấy được danh sách bản CUDA). Cài đặt vẫn tiếp tục với bản đã lưu.`;
    return;
  }
  if (!target) {
    box.classList.add("hidden");
    return;
  }
  box.classList.remove("hidden");
  box.textContent = "";

  const gpu = state.deps?.gpu;
  const verdict = document.createElement("strong");
  verdict.className = "cuda-support-verdict";
  box.classList.remove("is-good", "is-warn");

  // Only a capability we can actually read gets a verdict. An old driver
  // answers "[Not Supported]", which is truthy but unparseable — asserting
  // "✗ does not work" from it slandered cards that work fine.
  const known = /^\d+\.\d+$/.test(gpu?.capability ?? "");
  if (known && gpu) {
    const card = gpu.name || "GPU của bạn";
    if (target.supported) {
      verdict.textContent = `✓ ${card} (compute ${gpu.capability}) chạy được ${target.tag}`;
      box.classList.add("is-good");
    } else {
      const fits = cudaTargets.filter((t) => t.supported).map((t) => t.tag);
      verdict.textContent =
        `✗ ${card} (compute ${gpu.capability}) KHÔNG chạy được ${target.tag}` +
        (target.blocker ? `: ${target.blocker}` : "") +
        (fits.length ? ` — hãy chọn ${fits.join(" hoặc ")}` : "");
      box.classList.add("is-warn");
    }
  } else {
    verdict.textContent = `Các dòng card ${target.tag} hỗ trợ:`;
  }
  box.appendChild(verdict);

  const meta = document.createElement("span");
  meta.className = "cuda-support-meta";
  meta.textContent = `torch ${target.torch} · cần driver ${target.driver} · ${target.arches.join(", ")}`;
  box.appendChild(meta);

  const list = document.createElement("ul");
  for (const f of target.families) {
    const li = document.createElement("li");
    const name = document.createElement("b");
    name.textContent = f.name;
    li.append(name, document.createTextNode(` — ${f.cards}`));
    list.appendChild(li);
  }
  box.appendChild(list);
}

// ------------------------------------------------------------------ update

// Kept module-level rather than in `state`: nothing else reads it, and the
// banner needs the last status after the user dismisses and reopens it.
let updateStatus: UpdateStatus | null = null;

function renderUpdate(st: UpdateStatus) {
  updateStatus = st;

  const badge = $<HTMLButtonElement>("version-badge");
  badge.textContent = st.available ? `${st.current} → ${st.latest}` : st.current;
  badge.className = st.available ? "badge badge-btn is-update" : "badge badge-btn";
  badge.title = st.available
    ? `Đã có bản ${st.latest}`
    : st.reason || `Đang dùng bản mới nhất (${st.current})`;

  const banner = $("update-banner");
  banner.classList.toggle("hidden", !st.available);
  if (!st.available) return;

  $("update-banner-title").textContent = `Đã có bản ${st.latest}`;
  // When the update cannot be applied in place, `reason` explains why and the
  // button becomes a link to the release page instead of a broken action.
  $("update-banner-detail").textContent = st.canApply
    ? `Đang dùng ${st.current}. Tải ${st.assetName} (${bytes(st.assetSize)}) rồi khởi động lại.`
    : st.reason;
  $("update-banner-apply").textContent = st.canApply ? "Cập nhật" : "Mở trang tải";
}

async function checkForUpdate(quiet: boolean) {
  try {
    const st = (await api.CheckUpdate()) as UpdateStatus;
    renderUpdate(st);
    if (!quiet && !st.available) {
      toast(st.reason || `Đang dùng bản mới nhất (${st.current}).`, "good");
    }
  } catch (e) {
    // At startup a failed check is not worth a toast: no network is a normal
    // state for an app that works entirely offline once set up.
    if (quiet) return;
    toast(`Không kiểm tra được cập nhật: ${errText(e)}`, "error");
  }
}

async function applyUpdate() {
  if (!updateStatus?.available) return;
  if (!updateStatus.canApply) {
    // Not api.OpenPath: that one stats the target first, so it rejects URLs.
    BrowserOpenURL(updateStatus.url);
    return;
  }
  const btn = $<HTMLButtonElement>("update-banner-apply");
  btn.disabled = true;
  try {
    await api.ApplyUpdate();
    // On success the backend is already starting the new copy and closing this
    // one, so there is nothing left to render.
    toast("Đã cập nhật, đang khởi động lại…", "good");
  } catch (e) {
    toast(errText(e), "error");
    btn.disabled = false;
  }
}

function wireUpdate() {
  $("update-banner-apply").onclick = () => void applyUpdate();
  $("update-banner-dismiss").onclick = () => $("update-banner").classList.add("hidden");
  // Clicking the badge brings a dismissed banner back, and doubles as a manual
  // re-check when there is nothing to show.
  $("version-badge").onclick = () => {
    if (updateStatus?.available) {
      $("update-banner").classList.remove("hidden");
      return;
    }
    void checkForUpdate(false);
  };
}

// ------------------------------------------------------------------ layout

function initTabs() {
  $("tabs").addEventListener("click", (ev) => {
    const btn = (ev.target as HTMLElement).closest<HTMLButtonElement>(".tab");
    if (!btn) return;
    const name = btn.dataset.tab!;
    document
      .querySelectorAll<HTMLElement>(".tab")
      .forEach((t) => t.classList.toggle("is-active", t === btn));
    document.querySelectorAll<HTMLElement>(".panel").forEach((p) => {
      p.classList.toggle("hidden", p.dataset.panel !== name);
    });
  });
}

// renumberSteps keeps the studio headings sequential. Cards 2 and 4 are hidden
// until there is something to show, so the numbers have to be assigned from the
// currently visible set rather than hardcoded.
function renumberSteps() {
  let n = 0;
  document
    .querySelectorAll<HTMLElement>('[data-panel="studio"] [data-step]')
    .forEach((heading) => {
      const card = heading.closest<HTMLElement>(".card");
      if (card?.classList.contains("hidden")) {
        heading.textContent = heading.dataset.step!;
        return;
      }
      n += 1;
      heading.textContent = `${n} · ${heading.dataset.step}`;
    });
}

// ------------------------------------------------------------------ logging

function appendLog(line: LogLine) {
  const body = $("log-body");
  const atBottom =
    body.scrollHeight - body.scrollTop - body.clientHeight < 40;
  const row = document.createElement("div");
  row.className = `log-line is-${line.level}`;
  const time = document.createElement("span");
  time.className = "log-time";
  time.textContent = line.time;
  const text = document.createElement("span");
  text.className = "log-text";
  text.textContent = line.text;
  row.append(time, text);
  body.appendChild(row);
  while (body.childElementCount > 800) body.firstElementChild?.remove();
  if (atBottom) body.scrollTop = body.scrollHeight;
}

// ----------------------------------------------------------------- progress

function renderProgress(p: Progress) {
  const bar = $("progress");
  const fill = $("progress-fill");
  const isError = p.phase === "error";
  bar.classList.toggle("is-error", isError);
  bar.classList.toggle(
    "is-indeterminate",
    !isError && p.percent < 0 && p.phase !== "idle",
  );
  fill.style.width = p.percent >= 0 ? `${p.percent}%` : "";
  if (p.phase === "idle" && !p.label) {
    $("progress-label").textContent = "Sẵn sàng";
    $("progress-detail").textContent = "";
    fill.style.width = "0";
    return;
  }
  const pct = p.percent >= 0 ? ` ${p.percent.toFixed(0)}%` : "";
  $("progress-label").textContent = (p.label || "Đang xử lý") + pct;
  $("progress-detail").textContent = p.detail || "";
}

function setBusy(busy: boolean) {
  state.busy = busy;
  const ids = [
    "btn-info",
    "btn-download",
    "btn-pick-file",
    "btn-install-engines",
    "btn-update-ytdlp",
    "btn-refresh-roformer",
    "btn-recheck",
    "dep-banner-fix",
  ];
  ids.forEach((id) => ($(id) as HTMLButtonElement).disabled = busy);
  ($("btn-cancel") as HTMLButtonElement).disabled = !busy;
  document
    .querySelectorAll<HTMLButtonElement>("[data-model-action]")
    .forEach((b) => (b.disabled = busy));
  refreshSeparateButton();
}

// -------------------------------------------------------------------- deps

function renderDeps(report: DepsReport) {
  state.deps = report;
  $("models-dir").textContent = report.modelsDir;
  $("venv-dir").textContent = `${report.appDir}/pyenv`;

  const badge = $("gpu-badge");
  if (!report.gpu.checked) {
    badge.textContent = "Đang kiểm tra GPU…";
    badge.className = "badge";
  } else if (report.gpu.available) {
    // The backend is worth naming: "GPU" alone would leave an Apple Silicon
    // user unsure whether Metal or nothing at all is being used.
    const backend = report.gpu.backend === "mps" ? "MPS" : "CUDA";
    badge.textContent = `GPU · ${report.gpu.name || backend}`;
    badge.className = "badge is-good";
    badge.title = `${backend} · torch ${report.gpu.torch}`;
  } else {
    badge.textContent = "CPU only";
    badge.className = "badge is-warn";
    badge.title = report.gpu.reason || "Chưa tìm thấy PyTorch";
  }

  const list = $("dep-list");
  list.textContent = "";
  for (const tool of report.tools) {
    const row = document.createElement("div");
    row.className = "dep-row";

    const main = document.createElement("div");
    main.className = "dep-row-main";
    const title = document.createElement("div");
    title.className = "dep-row-title";
    title.append(document.createTextNode(tool.label));

    const pill = document.createElement("span");
    pill.className = `pill ${tool.found ? "is-ready" : "is-missing"}`;
    pill.textContent = tool.found ? "đã có" : tool.required ? "thiếu" : "chưa cài";
    title.appendChild(pill);
    if (tool.found && tool.version) {
      const ver = document.createElement("span");
      ver.className = "muted";
      ver.style.fontSize = "12px";
      ver.textContent = tool.version;
      title.appendChild(ver);
    }

    const sub = document.createElement("div");
    sub.className = "dep-row-sub";
    sub.textContent = tool.found
      ? `${tool.path}${tool.source ? `  ·  ${tool.source}` : ""}`
      : tool.hint;

    main.append(title, sub);
    row.appendChild(main);

    if (!tool.found && tool.canInstall) {
      const btn = document.createElement("button");
      btn.className = "btn btn-primary";
      btn.textContent = "Cài tự động";
      btn.onclick = () =>
        void (ENGINE_TOOLS.has(tool.id)
          ? installEngine(tool.id, tool.label)
          : installTool(tool.id, tool.label));
      row.appendChild(btn);
    }
    list.appendChild(row);
  }

  const missing = report.tools.filter((t) => t.required && !t.found);
  const banner = $("dep-banner");
  banner.classList.toggle("hidden", missing.length === 0);
  if (missing.length) {
    $("dep-banner-detail").textContent =
      `Chưa có ${missing.map((t) => t.label).join(", ")} — cần để tải và chuyển đổi audio.`;
  }
  refreshSeparateButton();

  // Bootstrap reports deps without probing the GPU, so the first table is
  // built with no capability and carries no verdict. Re-fetch exactly once per
  // capability change — comparing against what the table was built for, not
  // against whether anything is supported.
  if (report.gpu.capability !== cudaTargetsCapability) {
    // currentCudaTag(), not the raw select: this fires while the select can
    // still be empty — the very first deps event overtakes boot()'s own fetch —
    // and an empty `preferred` matches no option, so the list would settle on
    // the recommended index and the next autosave would write it over the tag
    // the user actually chose.
    void renderCudaTargets(currentCudaTag(), report.gpu.capability);
  }
}

// Engines are pip installs into the app's own venv, not standalone downloads,
// so their row button goes through InstallEngines instead of InstallTool —
// reusing the PyTorch and CUDA choices from the block further down the tab.
const ENGINE_TOOLS = new Set(["demucs", "audioSeparator"]);

async function installTool(id: string, label: string) {
  try {
    await api.InstallTool(id);
    toast(`Đã cài ${label}.`, "good");
    await reloadDeps();
  } catch (e) {
    toast(errText(e), "error");
  }
}

async function installEngine(id: string, label: string) {
  try {
    await api.InstallEngines({
      engines: [id],
      accel: $<HTMLSelectElement>("accel-select").value,
      cudaTag: currentCudaTag(),
    });
    toast(`Đã cài ${label}.`, "good");
    await reloadDeps(true);
    await reloadModels();
  } catch (e) {
    toast(errText(e), "error");
  }
}

async function reloadDeps(checkGPU = false) {
  const report = (await api.CheckDeps(checkGPU)) as DepsReport;
  renderDeps(report);
}

// ------------------------------------------------------------------ models

function groupModels(models: Model[]): Map<string, Model[]> {
  const groups = new Map<string, Model[]>();
  for (const m of models) {
    const list = groups.get(m.family) ?? [];
    list.push(m);
    groups.set(m.family, list);
  }
  return groups;
}

function renderModels(models: Model[]) {
  state.models = models;
  const select = $<HTMLSelectElement>("model-select");
  const previous = state.settings?.modelId ?? select.value;
  select.textContent = "";

  for (const [family, list] of groupModels(models)) {
    const group = document.createElement("optgroup");
    group.label = family;
    for (const m of list) {
      const opt = document.createElement("option");
      opt.value = m.id;
      opt.textContent = `${m.label}${m.installed ? "" : " — chưa tải"}`;
      group.appendChild(opt);
    }
    select.appendChild(group);
  }
  if (models.some((m) => m.id === previous)) select.value = previous;
  else if (models.length) select.value = models[0].id;

  renderModelNote();
  renderModelList(models);
}

function currentModel(): Model | undefined {
  const id = $<HTMLSelectElement>("model-select").value;
  return state.models.find((m) => m.id === id);
}

// twoStemsSelected reflects the Stem dropdown.
const twoStemsSelected = (): boolean =>
  $<HTMLSelectElement>("stems-select").value === "two";

// syncStemControl keeps the Stem dropdown honest. Only Demucs honours the
// setting; audio-separator always emits the model's own two stems, so leaving
// the control live for a RoFormer model silently does nothing.
function syncStemControl(m: Model | undefined) {
  const select = $<HTMLSelectElement>("stems-select");
  const applies = Boolean(m?.twoStemsOption);
  select.disabled = !applies;
  select.title = applies
    ? ""
    : "Model này luôn xuất đúng 2 stem — tuỳ chọn không áp dụng.";
}

// effectiveStems is what will actually land on disk, which is not the same as
// the model's full stem list once "Vocal + nhạc nền" is selected.
function effectiveStems(m: Model): string[] {
  if (m.twoStemsOption && twoStemsSelected()) return ["vocals", "no_vocals"];
  return m.stems ?? [];
}

function renderModelNote() {
  const m = currentModel();
  const note = $("model-note");
  note.textContent = "";
  syncStemControl(m);
  if (!m) return;

  const tag = document.createElement("span");
  tag.className = `tag ${m.installed ? "is-ready" : "is-missing"}`;
  tag.textContent = m.installed
    ? "đã tải"
    : m.sizeBytes > 0
      ? `cần tải ${bytes(m.sizeBytes)}`
      : "cần tải";
  note.appendChild(tag);

  const out = effectiveStems(m);
  const stems = out.length ? ` Sẽ xuất: ${out.join(", ")}.` : "";
  note.append(document.createTextNode(`${m.note}${stems}`));

  // Warn when the stem setting is quietly discarding what the model offers —
  // picking htdemucs_6s for guitar/piano and getting two files is the trap.
  const full = m.stems?.length ?? 0;
  if (m.twoStemsOption && twoStemsSelected() && full > 2) {
    const warn = document.createElement("span");
    warn.className = "tag is-missing";
    warn.style.marginLeft = "7px";
    warn.textContent = `đang bỏ ${full - 2} stem`;
    warn.title = `Model tách được ${full} stem. Đổi ô Stem sang "Tất cả stem" để lấy đủ.`;
    note.appendChild(warn);
  }
  refreshSeparateButton();
}

function renderModelList(models: Model[]) {
  const host = $("model-list");
  host.textContent = "";
  for (const [family, list] of groupModels(models)) {
    const heading = document.createElement("div");
    heading.className = "model-group-title";
    heading.textContent = family;
    host.appendChild(heading);

    for (const m of list) {
      const row = document.createElement("div");
      row.className = "model-row";

      const main = document.createElement("div");
      main.className = "model-row-main";
      const title = document.createElement("div");
      title.className = "model-row-title";
      title.append(document.createTextNode(m.label));
      const pill = document.createElement("span");
      pill.className = `pill ${m.installed ? "is-ready" : "is-missing"}`;
      pill.textContent = m.installed ? "đã tải" : "chưa tải";
      title.appendChild(pill);
      if (m.recommended) {
        const rec = document.createElement("span");
        rec.className = "pill is-rec";
        rec.textContent = "đề xuất";
        title.appendChild(rec);
      }
      const sub = document.createElement("div");
      sub.className = "model-row-sub";
      const size = m.sizeBytes > 0 ? `  ·  ${bytes(m.sizeBytes)}` : "";
      sub.textContent = `${m.note}${size}`;
      main.append(title, sub);
      row.appendChild(main);

      const btn = document.createElement("button");
      btn.className = m.installed ? "btn" : "btn btn-primary";
      btn.dataset.modelAction = m.id;
      btn.disabled = state.busy;
      btn.textContent = m.installed ? "Dùng model này" : "Tải model";
      btn.onclick = async () => {
        if (m.installed) {
          $<HTMLSelectElement>("model-select").value = m.id;
          await persistSettings({ modelId: m.id });
          renderModelNote();
          (document.querySelector('[data-tab="studio"]') as HTMLElement).click();
          return;
        }
        try {
          await api.EnsureModel(m.id);
          toast(`Đã tải ${m.label}.`, "good");
          await reloadModels();
        } catch (e) {
          toast(errText(e), "error");
        }
      };
      row.appendChild(btn);
      host.appendChild(row);
    }
  }
}

async function reloadModels() {
  renderModels((await api.ListModels()) as Model[]);
}

// ------------------------------------------------------------------ studio

function renderTrack(path: string, track: Track | null) {
  state.inputPath = path;
  state.track = track;
  const card = $("track-card");
  if (!path) {
    card.classList.add("hidden");
    renumberSteps();
    refreshSeparateButton();
    return;
  }
  card.classList.remove("hidden");
  renumberSteps();

  const info = track?.info;
  $("track-title").textContent =
    track?.title || path.split(/[\\/]/).pop() || path;

  const parts: string[] = [];
  if (info?.uploader) parts.push(info.uploader);
  const dur = track?.duration || info?.duration || 0;
  if (dur) parts.push(clock(dur));
  if (track?.sizeBytes) parts.push(bytes(track.sizeBytes));
  if (track?.format) parts.push(track.format.toUpperCase());
  $("track-sub").textContent = parts.join("  ·  ");
  $("track-path").textContent = path;

  const thumb = $<HTMLImageElement>("track-thumb");
  if (info?.thumbnail) {
    thumb.src = info.thumbnail;
    thumb.classList.remove("hidden");
  } else {
    thumb.removeAttribute("src");
    thumb.classList.add("hidden");
  }

  void api.MediaURL(path).then((url: string) => {
    $<HTMLAudioElement>("track-audio").src = url;
  });
  refreshSeparateButton();
}

function refreshSeparateButton() {
  const btn = $<HTMLButtonElement>("btn-separate");
  const model = currentModel();
  const hasInput = Boolean(state.inputPath);
  btn.disabled = state.busy || !hasInput || !model;
  if (!hasInput) {
    btn.textContent = "Tách vocal";
    return;
  }
  btn.textContent = model && !model.installed ? "Tải model & tách vocal" : "Tách vocal";
}

function renderResult(result: SeparateResult) {
  const card = $("result-card");
  card.classList.remove("hidden");
  renumberSteps();
  const model = state.models.find((m) => m.id === result.modelId);
  $("result-head").textContent =
    `${model?.label ?? result.modelName}  ·  ${clock(result.seconds)}  ·  ${result.dir}`;

  const host = $("stem-list");
  host.textContent = "";
  for (const stem of result.stems ?? []) {
    const isVocal = /vocal/i.test(stem.name) && !/no_vocal/i.test(stem.name);
    const box = document.createElement("div");
    box.className = `stem${isVocal ? " is-vocal" : ""}`;

    const top = document.createElement("div");
    top.className = "stem-top";
    const name = document.createElement("span");
    name.className = "stem-name";
    name.textContent = stem.name.replace(/_/g, " ");
    const size = document.createElement("span");
    size.className = "stem-size";
    size.textContent = bytes(stem.sizeBytes);
    const open = document.createElement("button");
    open.className = "link";
    open.textContent = "Mở file";
    open.onclick = () => void api.OpenPath(stem.path).catch((e) => toast(errText(e), "error"));
    top.append(name, size, open);

    const audio = document.createElement("audio");
    audio.controls = true;
    audio.preload = "none";
    void api.MediaURL(stem.path).then((url: string) => (audio.src = url));

    box.append(top, audio);
    host.appendChild(box);
  }

  $("btn-open-stems").onclick = () =>
    void api.OpenPath(result.dir).catch((e) => toast(errText(e), "error"));
  card.scrollIntoView({ behavior: "smooth", block: "nearest" });
}

// ---------------------------------------------------------------- settings

// selectOrFallback assigns a stored value to a <select> and reports whether it
// stuck, falling back when it did not.
//
// A select silently refuses a value with no matching <option> — it ends up as
// "" rather than throwing — and applyPlatformToInstallPanel removes the options
// this platform cannot use. Assigning blindly therefore left the control
// showing nothing, or showing index 0, while the store still held the old
// value and nothing reconciled them.
function selectOrFallback(id: string, value: string, fallback: string) {
  const select = $<HTMLSelectElement>(id);
  select.value = value;
  if (select.value !== value) select.value = fallback;
}

function fillSettings(s: Settings) {
  state.settings = s;
  $<HTMLSelectElement>("audio-format").value = s.audioFormat;
  selectOrFallback("device-select", s.device, "auto");
  $<HTMLSelectElement>("stems-select").value = s.twoStems ? "two" : "all";
  $<HTMLInputElement>("set-outdir").value = s.outputDir;
  $<HTMLSelectElement>("set-stemformat").value = s.stemFormat;
  $<HTMLSelectElement>("set-mp3").value = String(s.mp3Bitrate);
  $<HTMLSelectElement>("set-cookies").value = s.cookiesFromBrowser;
  // Only when one was actually chosen: an empty accel means the user has never
  // picked, and boot() asks the backend to suggest one instead.
  if (s.accel) selectOrFallback("accel-select", s.accel, "reuse");
  $<HTMLSelectElement>("cuda-tag").value = s.cudaTag;
  $<HTMLInputElement>("set-shifts").value = String(s.shifts);
  $<HTMLInputElement>("set-overlap").value = String(Math.round(s.overlap * 100));
  $<HTMLInputElement>("set-jobs").value = String(s.jobs);
  $<HTMLInputElement>("set-segment").value = String(s.segment);
  $<HTMLInputElement>("set-rf-seg").value = String(s.roformerSegmentSize);
  $<HTMLInputElement>("set-rf-overlap").value = String(s.roformerOverlap);
  $<HTMLInputElement>("set-rf-batch").value = String(s.roformerBatchSize);
  $<HTMLInputElement>("set-rf-norm").value = String(s.normalization);
  $<HTMLInputElement>("set-ytdlp").value = s.ytDlpPath;
  $<HTMLInputElement>("set-ffmpeg").value = s.ffmpegPath;
  $<HTMLInputElement>("set-python").value = s.pythonPath;
  $<HTMLInputElement>("set-demucs").value = s.demucsPath;
  $<HTMLInputElement>("set-audiosep").value = s.audioSeparatorPath;
  syncSliderLabels();
}

function syncSliderLabels() {
  $("lbl-shifts").textContent = `= ${$<HTMLInputElement>("set-shifts").value}`;
  $("lbl-overlap").textContent = `= ${$<HTMLInputElement>("set-overlap").value}%`;
  $("lbl-jobs").textContent = `= ${$<HTMLInputElement>("set-jobs").value}`;
}

// persistSettings merges a patch into the stored settings.
async function persistSettings(patch: Partial<Settings>) {
  if (!state.settings) return;
  const next = { ...state.settings, ...patch };
  try {
    state.settings = (await api.SaveSettings(next)) as Settings;
  } catch (e) {
    toast(errText(e), "error");
  }
}

function collectSettingsFromForm(): Partial<Settings> {
  return {
    outputDir: $<HTMLInputElement>("set-outdir").value.trim(),
    audioFormat: $<HTMLSelectElement>("audio-format").value,
    device: $<HTMLSelectElement>("device-select").value,
    twoStems: $<HTMLSelectElement>("stems-select").value === "two",
    stemFormat: $<HTMLSelectElement>("set-stemformat").value,
    mp3Bitrate: Number($<HTMLSelectElement>("set-mp3").value),
    cookiesFromBrowser: $<HTMLSelectElement>("set-cookies").value,
    accel: $<HTMLSelectElement>("accel-select").value,
    cudaTag: currentCudaTag(),
    shifts: Number($<HTMLInputElement>("set-shifts").value),
    overlap: Number($<HTMLInputElement>("set-overlap").value) / 100,
    jobs: Number($<HTMLInputElement>("set-jobs").value),
    segment: Number($<HTMLInputElement>("set-segment").value),
    roformerSegmentSize: Number($<HTMLInputElement>("set-rf-seg").value),
    roformerOverlap: Number($<HTMLInputElement>("set-rf-overlap").value),
    roformerBatchSize: Number($<HTMLInputElement>("set-rf-batch").value),
    normalization: Number($<HTMLInputElement>("set-rf-norm").value),
    ytDlpPath: $<HTMLInputElement>("set-ytdlp").value.trim(),
    ffmpegPath: $<HTMLInputElement>("set-ffmpeg").value.trim(),
    pythonPath: $<HTMLInputElement>("set-python").value.trim(),
    demucsPath: $<HTMLInputElement>("set-demucs").value.trim(),
    audioSeparatorPath: $<HTMLInputElement>("set-audiosep").value.trim(),
    modelId: $<HTMLSelectElement>("model-select").value,
  };
}

// Settings save on change rather than behind a Save button: every field is a
// simple preference and losing them to a forgotten click is worse.
function wireSettingsAutosave() {
  const selector =
    "#set-outdir, #audio-format, #device-select, #stems-select, #set-stemformat," +
    "#set-mp3, #set-cookies, #set-shifts, #set-overlap, #set-jobs, #set-segment," +
    "#set-rf-seg, #set-rf-overlap, #set-rf-batch, #set-rf-norm, #set-ytdlp," +
    "#set-ffmpeg, #set-python, #set-demucs, #set-audiosep, #model-select," +
    "#accel-select, #cuda-tag";
  let timer: number | undefined;
  const save = () => {
    window.clearTimeout(timer);
    timer = window.setTimeout(async () => {
      await persistSettings(collectSettingsFromForm());
      if (state.settings) fillSettings(state.settings);
    }, 250);
  };
  document.querySelectorAll<HTMLElement>(selector).forEach((el) => {
    el.addEventListener("change", save);
    if (el instanceof HTMLInputElement && el.type === "range") {
      el.addEventListener("input", syncSliderLabels);
    }
  });
  $("cuda-tag").addEventListener("change", () => {
    refreshInstallEnginesButton();
    renderCudaSupport();
  });
  $("model-select").addEventListener("change", renderModelNote);
  $("stems-select").addEventListener("change", renderModelNote);
}

// -------------------------------------------------------------------- about

function renderAbout(boot: Bootstrap) {
  const rows: Array<[string, string]> = [
    ["Phiên bản", boot.appVersion],
    ["Nền tảng", boot.platform],
    ["Số CPU", String(boot.cpus)],
    ["Thư mục dữ liệu", boot.appDir],
    ["Thư mục model", boot.deps.modelsDir],
    ["Thư mục binary", boot.deps.binDir],
  ];
  const dl = $("about-kv");
  dl.textContent = "";
  for (const [k, v] of rows) {
    const dt = document.createElement("dt");
    dt.textContent = k;
    const dd = document.createElement("dd");
    dd.textContent = v;
    dl.append(dt, dd);
  }
  $("btn-open-appdir").onclick = () =>
    void api.OpenPath(boot.appDir).catch((e) => toast(errText(e), "error"));
}

// ------------------------------------------------------------------ actions

function wireActions() {
  $("btn-info").onclick = async () => {
    const url = $<HTMLInputElement>("url").value.trim();
    if (!url) return toast("Hãy dán link YouTube hoặc Suno trước.", "error");
    try {
      const info = await api.FetchInfo(url);
      toast(`${info.title} — ${clock(info.duration)}`, "good");
      // Show the metadata immediately even though nothing is downloaded yet.
      if (!state.inputPath) {
        $("track-card").classList.remove("hidden");
        renumberSteps();
        $("track-title").textContent = info.title;
        $("track-sub").textContent = [info.uploader, clock(info.duration)]
          .filter(Boolean)
          .join("  ·  ");
        $("track-path").textContent = "chưa tải về";
        const thumb = $<HTMLImageElement>("track-thumb");
        if (info.thumbnail) {
          thumb.src = info.thumbnail;
          thumb.classList.remove("hidden");
        }
      }
    } catch (e) {
      toast(errText(e), "error");
    }
  };

  $("btn-download").onclick = async () => {
    const url = $<HTMLInputElement>("url").value.trim();
    if (!url) return toast("Hãy dán link YouTube hoặc Suno trước.", "error");
    $("result-card").classList.add("hidden");
    renumberSteps();
    try {
      const track = (await api.Download(url)) as Track;
      renderTrack(track.path, track);
      toast(`Đã tải: ${track.title}`, "good");
    } catch (e) {
      toast(errText(e), "error");
    }
  };

  $("btn-pick-file").onclick = async () => {
    try {
      const path = (await api.PickAudioFile()) as string;
      if (!path) return;
      $("result-card").classList.add("hidden");
      renderTrack(path, null);
      renumberSteps();
    } catch (e) {
      toast(errText(e), "error");
    }
  };

  $("btn-separate").onclick = async () => {
    const model = currentModel();
    if (!state.inputPath || !model) return;
    try {
      const result = (await api.Separate(state.inputPath, model.id)) as SeparateResult;
      renderResult(result);
      await reloadModels();
      toast(`Tách xong bằng ${model.label}.`, "good");
    } catch (e) {
      toast(errText(e), "error");
    }
  };

  $("btn-cancel").onclick = () => void api.Cancel();

  $("btn-open-track").onclick = () =>
    void api.OpenPath(state.inputPath).catch((e) => toast(errText(e), "error"));
  $("btn-reveal-track").onclick = () =>
    void api.RevealPath(state.inputPath).catch((e) => toast(errText(e), "error"));

  $("dep-banner-fix").onclick = async () => {
    const missing = (state.deps?.tools ?? []).filter(
      (t) => t.required && !t.found && t.canInstall,
    );
    for (const tool of missing) {
      try {
        // Only required tools reach here, and every one of those is a direct
        // download — no engine, so no InstallEngines branch needed.
        await api.InstallTool(tool.id);
      } catch (e) {
        toast(errText(e), "error");
        break;
      }
    }
    await reloadDeps();
  };

  $("btn-recheck").onclick = () =>
    void reloadDeps(true).catch((e) => toast(errText(e), "error"));

  $("btn-install-engines").onclick = async () => {
    const engines: string[] = [];
    if ($<HTMLInputElement>("eng-demucs").checked) engines.push("demucs");
    if ($<HTMLInputElement>("eng-roformer").checked) engines.push("audioSeparator");
    if (!engines.length) return toast("Chọn ít nhất một engine.", "error");
    try {
      await api.InstallEngines({
        engines,
        accel: $<HTMLSelectElement>("accel-select").value,
        cudaTag: currentCudaTag(),
      });
      toast("Cài engine xong.", "good");
      await reloadDeps(true);
      await reloadModels();
    } catch (e) {
      toast(errText(e), "error");
    }
  };

  $("btn-update-ytdlp").onclick = async () => {
    try {
      await api.UpdateYtDlp();
      toast("yt-dlp đã cập nhật.", "good");
      await reloadDeps();
    } catch (e) {
      toast(errText(e), "error");
    }
  };

  $("btn-refresh-roformer").onclick = async () => {
    try {
      const n = (await api.RefreshRoformerModels()) as number;
      await reloadModels();
      toast(n > 0 ? `Thêm ${n} model RoFormer.` : "Không có model mới.", "good");
    } catch (e) {
      toast(errText(e), "error");
    }
  };

  $("btn-pick-outdir").onclick = async () => {
    try {
      const dir = (await api.PickOutputDir()) as string;
      if (!dir) return;
      $<HTMLInputElement>("set-outdir").value = dir;
      await persistSettings({ outputDir: dir });
      toast("Đã đổi thư mục lưu.", "good");
    } catch (e) {
      toast(errText(e), "error");
    }
  };

  $("btn-toggle-log").onclick = () => {
    const pane = $("logpane");
    const hidden = pane.classList.toggle("hidden");
    $("btn-toggle-log").textContent = hidden ? "Log ▲" : "Log ▼";
    if (!hidden) $("log-body").scrollTop = $("log-body").scrollHeight;
  };

  $("btn-clear-log").onclick = () => {
    $("log-body").textContent = "";
    void api.ClearLog();
  };

  $<HTMLInputElement>("url").addEventListener("keydown", (ev) => {
    if (ev.key === "Enter" && !state.busy) $("btn-download").click();
  });
}

// ------------------------------------------------------------------- splash

// The window paints its markup before style.css is guaranteed to be applied —
// in dev the stylesheet arrives through the module graph, and either way the
// panels are still empty until Bootstrap answers. #splash covers both gaps with
// styling of its own (inline in index.html), and is torn down only once the
// real UI is both styled and filled.

const raf = () => new Promise<void>((r) => requestAnimationFrame(() => r()));

const sleep = (ms: number) => new Promise<void>((r) => window.setTimeout(r, ms));

// --bg only resolves once style.css is live, which makes it a direct probe for
// "the app's stylesheet is applied" — more precise than a load event, which
// fires per-<link> and says nothing in dev. Capped so a stylesheet that never
// arrives leaves a usable window rather than a permanent splash.
async function stylesReady(timeoutMs = 3000) {
  const applied = () =>
    getComputedStyle(document.documentElement).getPropertyValue("--bg").trim() !== "";
  const start = performance.now();
  while (!applied() && performance.now() - start < timeoutMs) await raf();

  // Fonts settle after the stylesheet, and swapping one in behind a revealed UI
  // reflows every label. Raced, because document.fonts.ready can stay pending
  // on a webview that never resolves it.
  await Promise.race([document.fonts?.ready ?? Promise.resolve(), sleep(1500)]);
}

async function hideSplash() {
  await stylesReady();
  document.body.dataset.appState = "ready";

  const splash = document.getElementById("splash");
  if (!splash) return;
  splash.classList.add("is-done");
  // Matches the 260ms opacity transition in index.html; a transitionend
  // listener would never fire under prefers-reduced-motion.
  await sleep(300);
  splash.remove();
}

// ---------------------------------------------------------------- bootstrap

async function boot() {
  initTabs();
  wireActions();
  wireUpdate();
  renumberSteps();

  EventsOn("app:log", (line: LogLine) => appendLog(line));
  EventsOn("app:progress", (p: Progress) => renderProgress(p));
  EventsOn("app:busy", (b: { busy: boolean }) => setBusy(b.busy));
  EventsOn("app:deps", (report: DepsReport) => renderDeps(report));

  try {
    const boot = (await api.Bootstrap()) as Bootstrap;
    // Strictly before fillSettings: it removes the <option>s this platform
    // cannot use, and removing an option that is already selected silently
    // resets its select to index 0. Run the other way round, a stored
    // device/accel was selected and then dropped, leaving the UI showing
    // "Tự động" while the store still held the old value — with autosave not
    // yet wired, nothing ever reconciled the two.
    applyPlatformToInstallPanel(boot.accels, boot.devices);
    fillSettings(boot.settings);
    renderModels(boot.models);
    // Claimed before renderDeps so its "capability changed, re-fetch" branch
    // sees the value it is about to be handed and stays quiet. Left at null, it
    // fired a second, unawaited renderCudaTargets that raced the awaited one
    // below — and lost the user's stored tag when it did, because it reads the
    // select, which is still empty this early.
    cudaTargetsCapability = boot.deps.gpu.capability;
    renderDeps(boot.deps);
    renderAbout(boot);
    // Seeded here rather than waiting for checkForUpdate, so the badge shows
    // the running version even when the check below never answers.
    $("version-badge").textContent = boot.appVersion;
    boot.log.forEach(appendLog);
    $("brand-sub").textContent = `YouTube · Suno → ${boot.settings.audioFormat.toUpperCase()} → tách vocal`;

    // Both selects must hold their real values before autosave is wired.
    // Wiring first meant any field change in the gap persisted whatever the
    // DOM happened to hold — cudaTag: "" while the options were still being
    // fetched, wiping a deliberate cu118/cu130, and accel frozen at the HTML
    // default "reuse" before the suggestion arrived, which is exactly the
    // pinned-to-a-broken-torch trap SuggestedAccel exists to avoid.
    await renderCudaTargets(boot.settings.cudaTag, boot.deps.gpu.capability);

    // A saved choice wins, so a deliberate "Tải bản CUDA (GPU)" is not
    // replaced by the suggestion on every launch.
    if (!boot.settings.accel) {
      $<HTMLSelectElement>("accel-select").value = (await api.SuggestedAccel()) as string;
    }

    wireSettingsAutosave();
  } catch (e) {
    toast(`Không khởi tạo được: ${errText(e)}`, "error");
  } finally {
    // In the finally, not the try: a failed Bootstrap still has to hand the
    // window over, otherwise the error toast is painted behind the splash and
    // the app looks hung.
    await hideSplash();
  }

  // Deliberately outside the try and not awaited: the update check reaches the
  // network, and neither a slow GitHub nor a missing one should hold up — or
  // fail — the rest of the startup.
  void checkForUpdate(true);
}

void boot();
