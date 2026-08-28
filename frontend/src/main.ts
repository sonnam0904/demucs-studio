import "./style.css";
import * as App from "../wailsjs/go/main/App";
import { EventsOn } from "../wailsjs/runtime/runtime";
import type {
  Bootstrap,
  DepsReport,
  LogLine,
  Model,
  Progress,
  SeparateResult,
  Settings,
  Track,
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
    badge.textContent = `GPU · ${report.gpu.name || "CUDA"}`;
    badge.className = "badge is-good";
    badge.title = `torch ${report.gpu.torch}`;
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
      if (tool.id === "ytdlp" || tool.id === "ffmpeg") {
        btn.textContent = "Cài tự động";
        btn.onclick = () => installTool(tool.id);
      } else {
        btn.textContent = "Xem cách cài";
        btn.onclick = () => {
          (document.querySelector('[data-tab="deps"]') as HTMLElement).click();
          toast("Dùng khối “Cài engine Python” bên dưới.", "info");
        };
      }
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
}

async function installTool(id: string) {
  try {
    await api.InstallTool(id);
    toast(`Đã cài ${id}.`, "good");
    await reloadDeps();
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

function fillSettings(s: Settings) {
  state.settings = s;
  $<HTMLSelectElement>("audio-format").value = s.audioFormat;
  $<HTMLSelectElement>("device-select").value = s.device;
  $<HTMLSelectElement>("stems-select").value = s.twoStems ? "two" : "all";
  $<HTMLInputElement>("set-outdir").value = s.outputDir;
  $<HTMLSelectElement>("set-stemformat").value = s.stemFormat;
  $<HTMLSelectElement>("set-mp3").value = String(s.mp3Bitrate);
  $<HTMLSelectElement>("set-cookies").value = s.cookiesFromBrowser;
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
    "#set-ffmpeg, #set-python, #set-demucs, #set-audiosep, #model-select";
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
    if (!url) return toast("Hãy dán URL YouTube trước.", "error");
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
    if (!url) return toast("Hãy dán URL YouTube trước.", "error");
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
        cudaTag: $<HTMLSelectElement>("cuda-tag").value,
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

// ---------------------------------------------------------------- bootstrap

async function boot() {
  initTabs();
  wireActions();
  renumberSteps();

  EventsOn("app:log", (line: LogLine) => appendLog(line));
  EventsOn("app:progress", (p: Progress) => renderProgress(p));
  EventsOn("app:busy", (b: { busy: boolean }) => setBusy(b.busy));
  EventsOn("app:deps", (report: DepsReport) => renderDeps(report));

  try {
    const boot = (await api.Bootstrap()) as Bootstrap;
    fillSettings(boot.settings);
    renderModels(boot.models);
    renderDeps(boot.deps);
    renderAbout(boot);
    boot.log.forEach(appendLog);
    $("brand-sub").textContent = `YouTube → ${boot.settings.audioFormat.toUpperCase()} → tách vocal`;
    wireSettingsAutosave();

    const accel = (await api.SuggestedAccel()) as string;
    $<HTMLSelectElement>("accel-select").value = accel;
  } catch (e) {
    toast(`Không khởi tạo được: ${errText(e)}`, "error");
  }
}

void boot();
