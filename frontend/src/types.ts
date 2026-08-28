// Shapes mirrored from the Go backend. Declared locally rather than imported
// from the generated bindings so the UI keeps compiling even when bindings are
// regenerated with slightly different naming.

export interface Settings {
  outputDir: string;
  ytDlpPath: string;
  ffmpegPath: string;
  pythonPath: string;
  demucsPath: string;
  audioSeparatorPath: string;
  audioFormat: string;
  cookiesFromBrowser: string;
  modelId: string;
  device: string;
  twoStems: boolean;
  shifts: number;
  overlap: number;
  segment: number;
  jobs: number;
  stemFormat: string;
  mp3Bitrate: number;
  roformerSegmentSize: number;
  roformerOverlap: number;
  roformerBatchSize: number;
  normalization: number;
}

export interface Tool {
  id: string;
  label: string;
  found: boolean;
  path: string;
  version: string;
  source: string;
  hint: string;
  argv: string[] | null;
  required: boolean;
  canInstall: boolean;
}

export interface GPU {
  checked: boolean;
  available: boolean;
  name: string;
  torch: string;
  reason: string;
}

export interface DepsReport {
  tools: Tool[];
  gpu: GPU;
  appDir: string;
  modelsDir: string;
  binDir: string;
  readyToRip: boolean;
}

export interface Model {
  id: string;
  backend: string;
  name: string;
  label: string;
  family: string;
  stems: string[] | null;
  note: string;
  sizeBytes: number;
  installed: boolean;
  recommended: boolean;
  supportsGpu: boolean;
  twoStemsOption: boolean;
}

export interface VideoInfo {
  id: string;
  title: string;
  uploader: string;
  duration: number;
  thumbnail: string;
  webpageUrl: string;
  isLive: boolean;
}

export interface Track {
  path: string;
  title: string;
  format: string;
  sizeBytes: number;
  duration: number;
  info: VideoInfo;
}

export interface Stem {
  name: string;
  path: string;
  sizeBytes: number;
}

export interface SeparateResult {
  modelId: string;
  modelName: string;
  dir: string;
  stems: Stem[] | null;
  seconds: number;
}

export interface LogLine {
  time: string;
  level: string;
  text: string;
}

export interface Progress {
  /** idle | error | info | download | model | separate | install */
  phase: string;
  percent: number;
  label: string;
  detail: string;
}

export interface UpdateStatus {
  current: string;
  latest: string;
  available: boolean;
  notes: string;
  url: string;
  // False when this install cannot overwrite itself — a .deb/.rpm, or a
  // directory the user cannot write. `reason` says which, ready to display.
  canApply: boolean;
  reason: string;
  assetUrl: string;
  assetName: string;
  assetSize: number;
}

export interface Bootstrap {
  settings: Settings;
  deps: DepsReport;
  models: Model[];
  log: LogLine[];
  platform: string;
  appDir: string;
  outputDir: string;
  cpus: number;
  appVersion: string;
}
