export namespace main {
	
	export class AiConfig {
	    provider: string;
	    apiKey: string;
	    baseUrl: string;
	    model: string;
	    temperature: number;
	    maxChars: number;
	
	    static createFrom(source: any = {}) {
	        return new AiConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.apiKey = source["apiKey"];
	        this.baseUrl = source["baseUrl"];
	        this.model = source["model"];
	        this.temperature = source["temperature"];
	        this.maxChars = source["maxChars"];
	    }
	}
	export class CommitRecord {
	    hash: string;
	    shortHash: string;
	    date: string;
	    author: string;
	    email: string;
	    message: string;
	    body: string;
	    refs: string;
	    branch: string;
	    branchInferred: boolean;
	    branchCount: number;
	    project: string;
	    repo: string;
	
	    static createFrom(source: any = {}) {
	        return new CommitRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.shortHash = source["shortHash"];
	        this.date = source["date"];
	        this.author = source["author"];
	        this.email = source["email"];
	        this.message = source["message"];
	        this.body = source["body"];
	        this.refs = source["refs"];
	        this.branch = source["branch"];
	        this.branchInferred = source["branchInferred"];
	        this.branchCount = source["branchCount"];
	        this.project = source["project"];
	        this.repo = source["repo"];
	    }
	}
	export class AnalyzePayload {
	    commits: CommitRecord[];
	    promptType: string;
	    customPrompt: string;
	    aiOverride?: AiConfig;
	
	    static createFrom(source: any = {}) {
	        return new AnalyzePayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.commits = this.convertValues(source["commits"], CommitRecord);
	        this.promptType = source["promptType"];
	        this.customPrompt = source["customPrompt"];
	        this.aiOverride = this.convertValues(source["aiOverride"], AiConfig);
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
	export class Usage {
	    prompt_tokens: number;
	    completion_tokens: number;
	    total_tokens: number;
	
	    static createFrom(source: any = {}) {
	        return new Usage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.prompt_tokens = source["prompt_tokens"];
	        this.completion_tokens = source["completion_tokens"];
	        this.total_tokens = source["total_tokens"];
	    }
	}
	export class ReportMeta {
	    provider: string;
	    model: string;
	    baseUrl: string;
	    commitCount: number;
	    contextTruncated: boolean;
	    contextUsed: number;
	    contextTotal: number;
	    range: string;
	    projects: string[];
	    fromReasoning: boolean;
	    usage?: Usage;
	
	    static createFrom(source: any = {}) {
	        return new ReportMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.baseUrl = source["baseUrl"];
	        this.commitCount = source["commitCount"];
	        this.contextTruncated = source["contextTruncated"];
	        this.contextUsed = source["contextUsed"];
	        this.contextTotal = source["contextTotal"];
	        this.range = source["range"];
	        this.projects = source["projects"];
	        this.fromReasoning = source["fromReasoning"];
	        this.usage = this.convertValues(source["usage"], Usage);
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
	export class AnalyzeResult {
	    content: string;
	    meta?: ReportMeta;
	
	    static createFrom(source: any = {}) {
	        return new AnalyzeResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.content = source["content"];
	        this.meta = this.convertValues(source["meta"], ReportMeta);
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
	export class ChatTurn {
	    role: string;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatTurn(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	    }
	}
	
	export class ScanConfig {
	    sinceDays: number;
	    maxDepth: number;
	    maxCommits: number;
	
	    static createFrom(source: any = {}) {
	        return new ScanConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sinceDays = source["sinceDays"];
	        this.maxDepth = source["maxDepth"];
	        this.maxCommits = source["maxCommits"];
	    }
	}
	export class Config {
	    ai: AiConfig;
	    workspaces: string[];
	    scan: ScanConfig;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ai = this.convertValues(source["ai"], AiConfig);
	        this.workspaces = source["workspaces"];
	        this.scan = this.convertValues(source["scan"], ScanConfig);
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
	export class Versions {
	    app: string;
	    wails: string;
	    go: string;
	    os: string;
	
	    static createFrom(source: any = {}) {
	        return new Versions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.app = source["app"];
	        this.wails = source["wails"];
	        this.go = source["go"];
	        this.os = source["os"];
	    }
	}
	export class PromptPresetItem {
	    value: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new PromptPresetItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.value = source["value"];
	        this.label = source["label"];
	    }
	}
	export class ProviderPreset {
	    value: string;
	    label: string;
	    baseUrl: string;
	    models: string[];
	
	    static createFrom(source: any = {}) {
	        return new ProviderPreset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.value = source["value"];
	        this.label = source["label"];
	        this.baseUrl = source["baseUrl"];
	        this.models = source["models"];
	    }
	}
	export class Meta {
	    providers: ProviderPreset[];
	    promptPresets: PromptPresetItem[];
	    configPath: string;
	    versions: Versions;
	
	    static createFrom(source: any = {}) {
	        return new Meta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providers = this.convertValues(source["providers"], ProviderPreset);
	        this.promptPresets = this.convertValues(source["promptPresets"], PromptPresetItem);
	        this.configPath = source["configPath"];
	        this.versions = this.convertValues(source["versions"], Versions);
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
	export class ModelListResult {
	    ok: boolean;
	    models: string[];
	    message: string;
	    baseUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelListResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.models = source["models"];
	        this.message = source["message"];
	        this.baseUrl = source["baseUrl"];
	    }
	}
	
	
	export class RefinePayload {
	    report: string;
	    requirement: string;
	    history: ChatTurn[];
	    promptType: string;
	    aiOverride?: AiConfig;
	
	    static createFrom(source: any = {}) {
	        return new RefinePayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.report = source["report"];
	        this.requirement = source["requirement"];
	        this.history = this.convertValues(source["history"], ChatTurn);
	        this.promptType = source["promptType"];
	        this.aiOverride = this.convertValues(source["aiOverride"], AiConfig);
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
	export class RepoInfo {
	    name: string;
	    path: string;
	    currentBranch: string;
	    remote: string;
	    remotes: string[];
	    commitCount: number;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new RepoInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.currentBranch = source["currentBranch"];
	        this.remote = source["remote"];
	        this.remotes = source["remotes"];
	        this.commitCount = source["commitCount"];
	        this.error = source["error"];
	    }
	}
	
	export class SaveResult {
	    ok: boolean;
	    canceled: boolean;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new SaveResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.canceled = source["canceled"];
	        this.path = source["path"];
	    }
	}
	
	export class ScanPayload {
	    sinceDays?: number;
	    since: string;
	    until: string;
	    workspaces: string[];
	    maxDepth: number;
	    maxCommits: number;
	
	    static createFrom(source: any = {}) {
	        return new ScanPayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sinceDays = source["sinceDays"];
	        this.since = source["since"];
	        this.until = source["until"];
	        this.workspaces = source["workspaces"];
	        this.maxDepth = source["maxDepth"];
	        this.maxCommits = source["maxCommits"];
	    }
	}
	export class ScanResult {
	    commits: CommitRecord[];
	    repos: RepoInfo[];
	    warnings: string[];
	    seq: number;
	    elapsedMs: number;
	    cached: boolean;
	    superseded: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ScanResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.commits = this.convertValues(source["commits"], CommitRecord);
	        this.repos = this.convertValues(source["repos"], RepoInfo);
	        this.warnings = source["warnings"];
	        this.seq = source["seq"];
	        this.elapsedMs = source["elapsedMs"];
	        this.cached = source["cached"];
	        this.superseded = source["superseded"];
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
	export class TestResult {
	    ok: boolean;
	    latency: number;
	    model: string;
	    baseUrl: string;
	    reply: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.latency = source["latency"];
	        this.model = source["model"];
	        this.baseUrl = source["baseUrl"];
	        this.reply = source["reply"];
	        this.message = source["message"];
	    }
	}
	

}

