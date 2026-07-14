// Configuration types that match the backend API structure

// Base configuration response from API
export interface ConfigResponse {
	server: ServerConfig;
	api: APIConfig;
	auth: AuthConfig;
	database: DatabaseConfig;
	metadata: MetadataConfig;
	streaming: StreamingConfig;
	transcoding: TranscodingConfig;
	health: HealthConfig;
	segment_cache: SegmentCacheConfig;
	import: ImportConfig;
	log: LogConfig;
	sabnzbd: SABnzbdConfig;
	arrs: ArrsConfig;
	stremio: StremioConfig;
	newznab: NewznabConfig;
	local_media: LocalMediaConfig;
	tube_tv: TubeTVConfig;
	players: TaterPlayersConfig;
	providers: ProviderConfig[];
	api_key?: string;
	download_key?: string;
	profiler_enabled: boolean;
}

// HTTP server bind configuration
export interface ServerConfig {
	port: number;
	host?: string;
}

// API server configuration
export interface APIConfig {
	prefix: string;
}

// Authentication configuration
export interface AuthConfig {
	login_required: boolean;
}

// Database configuration
export interface DatabaseConfig {
	type: string;
	path: string;
	dsn: string;
}

// Metadata configuration
export interface MetadataConfig {
	root_path: string;
	delete_source_nzb_on_removal?: boolean;
	backup: MetadataBackupConfig;
}

export interface MetadataBackupConfig {
	enabled?: boolean;
	schedule: string; // cron expression (UTC)
	keep_backups: number;
	path: string;
}

// Failure masking configuration
export interface FailureMaskingConfig {
	enabled: boolean;
	threshold: number;
}

// Streaming configuration
export interface StreamingConfig {
	max_prefetch: number;
	failure_masking: FailureMaskingConfig;
}

// FFmpeg transcoding configuration
export interface TranscodingConfig {
	enabled: boolean | null;
	profile: "crt_480p" | "hdmi_1080p" | "hdmi_4k" | string;
	hardware_acceleration:
		| "none"
		| "auto"
		| "vaapi"
		| "qsv"
		| "nvenc"
		| "videotoolbox"
		| "v4l2m2m"
		| string;
	ffmpeg_path: string;
	hardware_device?: string;
}

export interface TranscodingHardwareOption {
	id: string;
	label: string;
	available: boolean;
	device?: string;
	status: string;
	details?: string;
}

export interface TranscodingHardwareDetection {
	ffmpeg_path: string;
	ffmpeg_available: boolean;
	recommended: string;
	recommended_device?: string;
	current: string;
	current_device?: string;
	options: TranscodingHardwareOption[];
	notes?: string[];
}

// Segment cache configuration
export interface SegmentCacheConfig {
	enabled: boolean | null;
	cache_path: string;
	max_size_gb: number;
	expiry_hours: number;
}

// Health configuration
export interface HealthConfig {
	enabled?: boolean;
	library_dir?: string;
	cleanup_orphaned_metadata?: boolean;
	check_interval_seconds?: number;
	max_connections_for_health_checks?: number;
	check_batch_size?: number; // Files fetched and swept together per health-check cycle
	max_concurrent_jobs?: number; // Max concurrent health check jobs
	segment_sample_percentage?: number; // Percentage of segments to check (1-100)
	max_retries?: number; // Max health check retries
	library_sync_interval_minutes?: number; // Library sync interval in minutes (optional)
	library_sync_concurrency?: number;
	check_all_segments?: boolean; // Whether to check all segments or use sampling
	resolve_repair_on_import?: boolean; // Automatically resolve pending repairs in the same directory when a new file is imported
	verify_data?: boolean; // Verify 1 byte of data for each segment
	read_timeout_seconds?: number; // Timeout for data verification
	acceptable_missing_segments_percentage?: number;
	repair: RepairConfig;
	// What happens when the health checker or a streaming read confirms real
	// (non-degraded) corruption: "repair" (default) triggers an Arr rescan;
	// "delete" removes the file and cleans up now-empty parent directories instead.
	corruption_action?: "repair" | "delete";
}

export interface RepairConfig {
	enabled: boolean;
	interval_minutes: number;
	max_cooldown_hours: number;
	max_repair_retries: number; // Max repair notification retries
	exponential_backoff: boolean;
}

// Dry run result for library sync
export interface DryRunSyncResult {
	orphaned_metadata_count: number; // Number of orphaned metadata files
	orphaned_library_files: number; // Number of orphaned library files (symlinks/STRM)
	database_records_to_clean: number; // Number of database records to clean
	would_cleanup: boolean; // Whether cleanup would occur based on config
}

// Import strategy type
export type ImportStrategy = "NONE" | "SYMLINK" | "STRM";

// Import configuration
export interface ImportConfig {
	max_processor_workers: number;
	queue_processing_interval_seconds: number; // Interval in seconds for queue processing
	allowed_file_extensions: string[];
	max_download_prefetch: number;
	segment_sample_percentage: number; // Percentage of segments to check (1-100)
	read_timeout_seconds: number;
	import_strategy: ImportStrategy;
	import_dir?: string | null;
	watch_dir?: string | null;
	watch_interval_seconds?: number | null;
	allow_nested_rar_extraction?: boolean;
	rename_to_nzb_name?: boolean;
	filter_sample_files?: boolean;
	failed_item_retention_hours?: number | null;
	history_retention_days?: number | null;
}

// Log configuration
export interface LogConfig {
	file: string;
	level: string;
	max_size: number;
	max_age: number;
	max_backups: number;
	compress: boolean;
}

// NNTP Provider configuration (sanitized)
export interface ProviderConfig {
	id: string;
	name?: string;
	host: string;
	port: number;
	username: string;
	max_connections: number;
	inflight_requests: number;
	stat_inflight_requests: number;
	tls: boolean;
	insecure_tls: boolean;
	proxy_url?: string;
	password_set: boolean;
	enabled: boolean;
	is_backup_provider: boolean;
	skip_ping?: boolean;
	keepalive_interval_seconds?: number;
	keepalive_command?: string;
	user_agent?: string;
	quota_bytes?: number;
	quota_period_hours?: number;
	last_rtt_ms?: number;
	last_speed_test_mbps?: number;
	last_speed_test_time?: string;
	account_expiration_date?: string;
}

// Pipeline auto-tune result for a single provider
export interface PipelineDepthSample {
	depth: number;
	mbps: number;
}

export interface PipelineTuneResponse {
	recommended_inflight: number;
	baseline_mbps: number;
	best_mbps: number;
	improvement_pct: number;
	enabled: boolean;
	test_connections: number;
	tested: PipelineDepthSample[];
	warning?: string;
}

// SABnzbd configuration
export interface SABnzbdConfig {
	enabled: boolean;
	complete_dir: string;
	download_client_base_url?: string;
	categories: SABnzbdCategory[];
	history_retention_minutes: number;
	fallback_host?: string;
	fallback_api_key?: string; // Obfuscated when returned from API
	fallback_api_key_set?: boolean; // For display purposes only
}

// SABnzbd category configuration
export interface SABnzbdCategory {
	name: string;
	order: number;
	priority: number;
	dir: string;
}

// Configuration update request types
export interface ConfigUpdateRequest {
	server?: ServerUpdateRequest;
	api?: APIUpdateRequest;
	auth?: AuthUpdateRequest;
	database?: DatabaseUpdateRequest;
	metadata?: MetadataUpdateRequest;
	streaming?: StreamingUpdateRequest;
	transcoding?: Partial<TranscodingConfig>;
	segment_cache?: Partial<SegmentCacheConfig>;
	health?: HealthUpdateRequest;
	import?: ImportUpdateRequest;
	log?: LogUpdateRequest;
	sabnzbd?: SABnzbdUpdateRequest;
	arrs?: ArrsConfig;
	stremio?: Partial<StremioConfig>;
	newznab?: Partial<NewznabConfig>;
	local_media?: Partial<LocalMediaConfig>;
	tube_tv?: Partial<TubeTVConfig>;
	providers?: ProviderUpdateRequest[];
	profiler_enabled?: boolean;
}

// Server update request
export interface ServerUpdateRequest {
	port?: number;
	host?: string;
}

// API update request
export interface APIUpdateRequest {
	prefix?: string;
}

// Auth update request
export interface AuthUpdateRequest {
	login_required?: boolean;
}

// Database update request
export interface DatabaseUpdateRequest {
	type?: string;
	path?: string;
	dsn?: string;
}

// Metadata update request
export interface MetadataUpdateRequest {
	root_path?: string;
	delete_source_nzb_on_removal?: boolean;
	backup?: MetadataBackupConfig;
}

// Streaming update request
export interface StreamingUpdateRequest {
	max_prefetch?: number;
	failure_masking?: Partial<FailureMaskingConfig>;
}

// Health update request
export interface HealthUpdateRequest {
	enabled?: boolean;
	library_dir?: string;
	cleanup_orphaned_metadata?: boolean;
	check_interval_seconds?: number; // Interval in seconds (optional)
	max_connections_for_health_checks?: number;
	check_batch_size?: number; // Files fetched and swept together per health-check cycle
	max_concurrent_jobs?: number; // Max concurrent health check jobs
	segment_sample_percentage?: number; // Percentage of segments to check (1-100)
	max_retries?: number;
	read_timeout_seconds?: number;
	library_sync_interval_minutes?: number; // Library sync interval in minutes (optional)
	library_sync_concurrency?: number;
	check_all_segments?: boolean; // Whether to check all segments or use sampling
	resolve_repair_on_import?: boolean;
	verify_data?: boolean;
	acceptable_missing_segments_percentage?: number;
	repair?: Partial<RepairConfig>;
	corruption_action?: "repair" | "delete";
}

// Import update request
export interface ImportUpdateRequest {
	max_processor_workers?: number;
	queue_processing_interval_seconds?: number; // Interval in seconds for queue processing
	allowed_file_extensions?: string[];
	import_strategy?: ImportStrategy;
	import_dir?: string | null;
	watch_dir?: string | null;
	watch_interval_seconds?: number | null;
	allow_nested_rar_extraction?: boolean;
	rename_to_nzb_name?: boolean;
	filter_sample_files?: boolean;
}

// Log update request
export interface LogUpdateRequest {
	file?: string;
	level?: string;
	max_size?: number;
	max_age?: number;
	max_backups?: number;
	compress?: boolean;
}

// Provider update request
export interface ProviderUpdateRequest {
	name?: string;
	host?: string;
	port?: number;
	username?: string;
	password?: string;
	max_connections?: number;
	inflight_requests?: number;
	stat_inflight_requests?: number;
	tls?: boolean;
	insecure_tls?: boolean;
	proxy_url?: string;
	enabled?: boolean;
	is_backup_provider?: boolean;
	skip_ping?: boolean;
	keepalive_interval_seconds?: number;
	keepalive_command?: string;
	user_agent?: string;
	quota_bytes?: number;
	quota_period_hours?: number;
	account_expiration_date?: string;
}

// SABnzbd update request
export interface SABnzbdUpdateRequest {
	enabled?: boolean;
	complete_dir?: string;
	categories?: SABnzbdCategory[];
	history_retention_minutes?: number;
	fallback_host?: string;
	fallback_api_key?: string;
}

// Configuration section names for PATCH requests
export type ConfigSection =
	| "server"
	| "auth"
	| "metadata"
	| "streaming"
	| "transcoding"
	| "segment_cache"
	| "health"
	| "import"
	| "providers"
	| "sabnzbd"
	| "arrs"
	| "stremio"
	| "newznab"
	| "local_media"
	| "tube_tv"
	| "players"
	| "system";

export interface ProviderFormData {
	name: string;
	host: string;
	port: number;
	username: string;
	password: string;
	max_connections: number;
	inflight_requests: number;
	stat_inflight_requests: number;
	tls: boolean;
	insecure_tls: boolean;
	proxy_url: string;
	enabled: boolean;
	is_backup_provider: boolean;
	skip_ping: boolean;
	keepalive_interval_seconds: number;
	keepalive_command: string;
	user_agent: string;
	quota_bytes: number;
	quota_period_hours: number;
	account_expiration_date: string;
}

export interface LogFormData {
	file: string;
	level: string;
	max_size: number;
	max_age: number;
	max_backups: number;
	compress: boolean;
}

// Arrs configuration types
export type ArrsType = "radarr" | "sonarr" | "lidarr" | "readarr" | "whisparr" | "sportarr";

export interface ArrsInstanceConfig {
	name: string;
	url: string;
	api_key: string;
	category?: string;
	enabled: boolean;
	sync_interval_hours: number;
}

export interface ArrsConfig {
	enabled: boolean;
	max_workers: number;
	webhook_base_url?: string;
	radarr_instances: ArrsInstanceConfig[];
	sonarr_instances: ArrsInstanceConfig[];
	lidarr_instances: ArrsInstanceConfig[];
	readarr_instances: ArrsInstanceConfig[];
	whisparr_instances: ArrsInstanceConfig[];
	sportarr_instances: ArrsInstanceConfig[];
	queue_cleanup_enabled?: boolean;
	queue_cleanup_interval_seconds?: number;
	queue_cleanup_grace_period_minutes?: number;
	queue_cleanup_max_failures?: number;
	queue_cleanup_rules?: StuckCleanupRule[];
}

export type StuckCleanupAction = "remove" | "blocklist" | "blocklist_search";

export interface StuckCleanupRule {
	message: string;
	enabled: boolean;
	action: StuckCleanupAction;
}

// Prowlarr indexer configuration (nested inside StremioConfig)
export interface ProwlarrConfig {
	enabled: boolean;
	host: string;
	api_key: string;
	categories: number[];
	languages: string[];
	qualities: string[];
}

// Stremio integration configuration
export interface StremioConfig {
	enabled: boolean;
	nzb_ttl_hours: number;
	base_url?: string;
	prowlarr: ProwlarrConfig;
}

// Newznab catalog configuration used by Tater Tube players.
export interface NewznabConfig {
	enabled: boolean;
	url: string;
	api_key: string;
	api_key_set?: boolean;
	username?: string;
	browse_limit: number;
	watch_again_limit: number;
	watch_again_retention_days: number;
}

export interface LocalMediaCategory {
	id: string;
	name: string;
	library_type?: "movies" | "tv" | "music" | "folders" | string;
	paths: string[];
	enabled?: boolean;
}

export interface LocalMediaConfig {
	enabled: boolean;
	categories: LocalMediaCategory[];
}

export interface TubeTVCustomSource {
	category_id: string;
	source_index: number;
	path: string;
	title?: string;
	media_type?: string;
}

export interface TubeTVCustomChannel {
	id: string;
	title: string;
	commercial_category?: string;
	sources: TubeTVCustomSource[];
}

export interface TubeTVConfig {
	enabled: boolean;
	auto_channels: boolean;
	commercials_enabled: boolean;
	midroll_commercials: boolean;
	commercial_categories: string[];
	commercials_path: string;
	custom_channels: TubeTVCustomChannel[];
}

export interface TubeTVCommercialVideo {
	title: string;
	categoryId: string;
	category: string;
	name: string;
	url?: string;
	kind: string;
	local: boolean;
	duration: number;
	fullDuration: number;
}

export interface TubeTVCommercialCategory {
	id: string;
	title: string;
	count: number;
	videos: TubeTVCommercialVideo[];
}

export interface TubeTVCommercialLibrary {
	root: string;
	categories: TubeTVCommercialCategory[];
}

export interface TubeTVLocalLibraryRow {
	id?: string;
	title: string;
	detail?: string;
	type?: string;
	mediaType?: string;
	categoryId?: string;
	sourceIndex: number;
	path?: string;
	count?: number;
	selectable: boolean;
	browsable: boolean;
}

export interface TubeTVLocalLibraryResponse {
	title: string;
	categoryId?: string;
	sourceIndex: number;
	path?: string;
	source?: TubeTVCustomSource;
	rows: TubeTVLocalLibraryRow[];
}

export interface TubeTVGuideScheduleItem {
	title?: string;
	kind?: string;
	type?: string;
	mediaType?: string;
	category?: string;
	categoryId?: string;
	path?: string;
	start?: number;
	end?: number;
	duration?: number;
	fullDuration?: number;
	mediaOffset?: number;
	durationKnown?: boolean;
	forceAdvance?: boolean;
	local?: boolean;
}

export interface TubeTVGuideChannel {
	number: string;
	title: string;
	streamUrl?: string;
	totalDuration: number;
	schedule: TubeTVGuideScheduleItem[];
}

export interface TubeTVGuideResponse {
	channels: TubeTVGuideChannel[];
	startedAt?: string;
	generatedAt?: string;
	updatedAt?: string;
	plannedUntil?: string;
	horizonHours: number;
	refillThresholdHours: number;
}

export interface TaterPlayer {
	id: string;
	name: string;
	created_at: string;
	last_seen_at?: string;
	revoked_at?: string;
}

export interface TaterPairingCode {
	id: string;
	name?: string;
	code?: string;
	created_at: string;
	expires_at: string;
}

export interface TaterPlayersConfig {
	players: TaterPlayer[];
	pairing_codes: TaterPairingCode[];
}

export interface TaterPairingCodeCreateResponse extends TaterPairingCode {
	code: string;
}

// Helper type for configuration sections
interface ConfigSectionInfo {
	title: string;
	description: string;
	icon: string;
	canEdit: boolean;
	hidden?: boolean;
}

// Configuration sections metadata
// Provider management types
export interface ProviderTestRequest {
	provider_id?: string;
	host: string;
	port: number;
	username: string;
	password: string;
	tls: boolean;
	insecure_tls: boolean;
	proxy_url?: string;
	skip_ping?: boolean;
}

export interface ProviderTestResponse {
	success: boolean;
	error_message?: string;
	rtt_ms?: number;
}

export interface ProviderCreateRequest {
	name?: string;
	host: string;
	port: number;
	username: string;
	password: string;
	max_connections: number;
	inflight_requests?: number;
	stat_inflight_requests?: number;
	tls: boolean;
	insecure_tls: boolean;
	proxy_url?: string;
	enabled: boolean;
	is_backup_provider: boolean;
	skip_ping?: boolean;
	keepalive_interval_seconds?: number;
	keepalive_command?: string;
	user_agent?: string;
	quota_bytes?: number;
	quota_period_hours?: number;
	account_expiration_date?: string;
}

export interface ProviderReorderRequest {
	provider_ids: string[];
}

export const CONFIG_SECTIONS: Record<ConfigSection | "system", ConfigSectionInfo> = {
	server: {
		title: "Server",
		description: "Configure the web UI and API host/port.",
		icon: "Globe",
		canEdit: true,
		hidden: true,
	},
	auth: {
		title: "Authentication",
		description: "User authentication and login settings",
		icon: "Shield",
		canEdit: true,
	},
	metadata: {
		title: "Stream Metadata",
		description: "Configure where processed NZB metadata and streamer cache state are stored.",
		icon: "Folder",
		canEdit: true,
	},
	streaming: {
		title: "Streaming",
		description: "Segment prefetch and on-disk cache settings for smoother media playback.",
		icon: "Download",
		canEdit: true,
	},
	transcoding: {
		title: "Hardware Transcoding",
		description:
			"FFmpeg playback conversion profiles and hardware acceleration for Stream and Local media.",
		icon: "Cpu",
		canEdit: true,
	},
	segment_cache: {
		title: "Segment Cache",
		description: "Segment-aligned disk cache for smoother media playback.",
		icon: "HardDrive",
		canEdit: true,
		hidden: true,
	},
	health: {
		title: "Health Monitoring",
		description: "File health monitoring and automatic repair settings",
		icon: "Shield",
		canEdit: true,
		hidden: true,
	},
	import: {
		title: "NZB Processing",
		description: "Configure workers that prepare NZB releases for streaming.",
		icon: "Cog",
		canEdit: true,
	},
	providers: {
		title: "NNTP Providers",
		description: "Usenet provider configuration used by the streaming engine.",
		icon: "Radio",
		canEdit: true,
	},
	sabnzbd: {
		title: "SABnzbd API",
		description:
			"Emulate a SABnzbd server to allow ARR applications to send NZBs to Tater Tube Server.",
		icon: "Download",
		canEdit: true,
		hidden: true,
	},
	arrs: {
		title: "ARR Management",
		description:
			"Configure Radarr, Sonarr, Lidarr, Readarr, and Whisparr instances for media file synchronization and automatic repair.",
		icon: "Cog",
		canEdit: true,
		hidden: true,
	},
	stremio: {
		title: "Stremio Integration",
		description: "Enable the Stremio addon and direct NZB stream endpoint.",
		icon: "Tv",
		canEdit: true,
		hidden: true,
	},
	newznab: {
		title: "Newznab Stream",
		description: "Configure the player-facing Stream catalog for Tater Tube.",
		icon: "Link",
		canEdit: true,
	},
	local_media: {
		title: "Local Media",
		description: "Expose server-local folders as The Tube categories.",
		icon: "Folder",
		canEdit: true,
	},
	tube_tv: {
		title: "Tube TV",
		description: "Server-side The Tube channels, custom lineups, and commercial breaks.",
		icon: "Tv",
		canEdit: true,
	},
	players: {
		title: "Tater Tube Players",
		description: "Pair, view, and revoke Tater Tube player devices.",
		icon: "Tv",
		canEdit: true,
	},
	system: {
		title: "System",
		description: "System settings",
		icon: "HardDrive",
		canEdit: true,
	},
};
