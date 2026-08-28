export namespace bus {
	
	export class LogLine {
	    time: string;
	    level: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new LogLine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.level = source["level"];
	        this.text = source["text"];
	    }
	}

}

export namespace deps {
	
	export class EngineSpec {
	    engines: string[];
	    accel: string;
	    cudaTag: string;
	
	    static createFrom(source: any = {}) {
	        return new EngineSpec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.engines = source["engines"];
	        this.accel = source["accel"];
	        this.cudaTag = source["cudaTag"];
	    }
	}
	export class GPU {
	    checked: boolean;
	    available: boolean;
	    name: string;
	    torch: string;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new GPU(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.checked = source["checked"];
	        this.available = source["available"];
	        this.name = source["name"];
	        this.torch = source["torch"];
	        this.reason = source["reason"];
	    }
	}
	export class Tool {
	    id: string;
	    label: string;
	    found: boolean;
	    path: string;
	    version: string;
	    source: string;
	    hint: string;
	    argv: string[];
	    required: boolean;
	    canInstall: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Tool(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.found = source["found"];
	        this.path = source["path"];
	        this.version = source["version"];
	        this.source = source["source"];
	        this.hint = source["hint"];
	        this.argv = source["argv"];
	        this.required = source["required"];
	        this.canInstall = source["canInstall"];
	    }
	}
	export class Report {
	    tools: Tool[];
	    gpu: GPU;
	    appDir: string;
	    modelsDir: string;
	    binDir: string;
	    readyToRip: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Report(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tools = this.convertValues(source["tools"], Tool);
	        this.gpu = this.convertValues(source["gpu"], GPU);
	        this.appDir = source["appDir"];
	        this.modelsDir = source["modelsDir"];
	        this.binDir = source["binDir"];
	        this.readyToRip = source["readyToRip"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace engine {
	
	export class Model {
	    id: string;
	    backend: string;
	    name: string;
	    label: string;
	    family: string;
	    stems: string[];
	    note: string;
	    sizeBytes: number;
	    installed: boolean;
	    recommended: boolean;
	    supportsGpu: boolean;
	    twoStemsOption: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Model(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.backend = source["backend"];
	        this.name = source["name"];
	        this.label = source["label"];
	        this.family = source["family"];
	        this.stems = source["stems"];
	        this.note = source["note"];
	        this.sizeBytes = source["sizeBytes"];
	        this.installed = source["installed"];
	        this.recommended = source["recommended"];
	        this.supportsGpu = source["supportsGpu"];
	        this.twoStemsOption = source["twoStemsOption"];
	    }
	}
	export class Stem {
	    name: string;
	    path: string;
	    sizeBytes: number;
	
	    static createFrom(source: any = {}) {
	        return new Stem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.sizeBytes = source["sizeBytes"];
	    }
	}
	export class Result {
	    modelId: string;
	    modelName: string;
	    dir: string;
	    stems: Stem[];
	    seconds: number;
	
	    static createFrom(source: any = {}) {
	        return new Result(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.modelId = source["modelId"];
	        this.modelName = source["modelName"];
	        this.dir = source["dir"];
	        this.stems = this.convertValues(source["stems"], Stem);
	        this.seconds = source["seconds"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {
	
	export class Bootstrap {
	    settings: settings.Settings;
	    deps: deps.Report;
	    models: engine.Model[];
	    log: bus.LogLine[];
	    platform: string;
	    appDir: string;
	    outputDir: string;
	    cpus: number;
	    appVersion: string;
	
	    static createFrom(source: any = {}) {
	        return new Bootstrap(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.settings = this.convertValues(source["settings"], settings.Settings);
	        this.deps = this.convertValues(source["deps"], deps.Report);
	        this.models = this.convertValues(source["models"], engine.Model);
	        this.log = this.convertValues(source["log"], bus.LogLine);
	        this.platform = source["platform"];
	        this.appDir = source["appDir"];
	        this.outputDir = source["outputDir"];
	        this.cpus = source["cpus"];
	        this.appVersion = source["appVersion"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace settings {
	
	export class Settings {
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
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.outputDir = source["outputDir"];
	        this.ytDlpPath = source["ytDlpPath"];
	        this.ffmpegPath = source["ffmpegPath"];
	        this.pythonPath = source["pythonPath"];
	        this.demucsPath = source["demucsPath"];
	        this.audioSeparatorPath = source["audioSeparatorPath"];
	        this.audioFormat = source["audioFormat"];
	        this.cookiesFromBrowser = source["cookiesFromBrowser"];
	        this.modelId = source["modelId"];
	        this.device = source["device"];
	        this.twoStems = source["twoStems"];
	        this.shifts = source["shifts"];
	        this.overlap = source["overlap"];
	        this.segment = source["segment"];
	        this.jobs = source["jobs"];
	        this.stemFormat = source["stemFormat"];
	        this.mp3Bitrate = source["mp3Bitrate"];
	        this.roformerSegmentSize = source["roformerSegmentSize"];
	        this.roformerOverlap = source["roformerOverlap"];
	        this.roformerBatchSize = source["roformerBatchSize"];
	        this.normalization = source["normalization"];
	    }
	}

}

export namespace ytdl {
	
	export class Info {
	    id: string;
	    title: string;
	    uploader: string;
	    duration: number;
	    thumbnail: string;
	    webpageUrl: string;
	    isLive: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.uploader = source["uploader"];
	        this.duration = source["duration"];
	        this.thumbnail = source["thumbnail"];
	        this.webpageUrl = source["webpageUrl"];
	        this.isLive = source["isLive"];
	    }
	}
	export class Track {
	    path: string;
	    title: string;
	    format: string;
	    sizeBytes: number;
	    duration: number;
	    info: Info;
	
	    static createFrom(source: any = {}) {
	        return new Track(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.title = source["title"];
	        this.format = source["format"];
	        this.sizeBytes = source["sizeBytes"];
	        this.duration = source["duration"];
	        this.info = this.convertValues(source["info"], Info);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

