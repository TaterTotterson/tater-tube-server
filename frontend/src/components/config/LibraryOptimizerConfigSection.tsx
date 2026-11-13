import {
	AlertTriangle,
	ArchiveRestore,
	CheckCircle2,
	ChevronLeft,
	ChevronRight,
	Clock3,
	DatabaseZap,
	Gauge,
	HardDrive,
	History,
	Info,
	ListChecks,
	Pause,
	Play,
	RefreshCw,
	Save,
	Search,
	Settings2,
	ShieldCheck,
	Sparkles,
	Square,
	Volume2,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { apiClient } from "../../api/client";
import type {
	ConfigResponse,
	LibraryOptimizerAnalysis,
	LibraryOptimizerCapabilities,
	LibraryOptimizerConfig,
	LibraryOptimizerHistory,
	LibraryOptimizerHistoryRecord,
	LibraryOptimizerStatus,
} from "../../types/config";

interface LibraryOptimizerConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: LibraryOptimizerConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

const DEFAULTS: LibraryOptimizerConfig = {
	enabled: false,
	automatic: false,
	target_codec: "hevc",
	quality: "balanced",
	audio_policy: "best_english",
	audio_format: "copy",
	subtitle_policy: "forced_english",
	pause_during_playback: true,
	skip_dolby_vision: true,
	minimum_savings_percent: 15,
	quarantine_days: 7,
};

type OptimizerTab = "setup" | "activity" | "history";

const HISTORY_PAGE_SIZE = 50;

function normalized(value?: LibraryOptimizerConfig): LibraryOptimizerConfig {
	return { ...DEFAULTS, ...value };
}

function bytes(value = 0) {
	if (!Number.isFinite(value) || value <= 0) return "0 B";
	const units = ["B", "KB", "MB", "GB", "TB"];
	const power = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
	return `${(value / 1024 ** power).toFixed(power > 2 ? 2 : 1)} ${units[power]}`;
}

function mediaName(path: string) {
	const parts = path.split(/[\\/]/).filter(Boolean);
	return parts.at(-1) || path;
}

function historyStatusClass(status: string) {
	switch (status) {
		case "completed":
			return "badge-success badge-outline";
		case "failed":
			return "badge-error badge-outline";
		default:
			return "badge-warning badge-outline";
	}
}

function historySavings(record: LibraryOptimizerHistoryRecord) {
	if (record.status !== "completed" || !record.saved_bytes) return "";
	const percent = record.original_bytes
		? Math.max(0, Math.round((record.saved_bytes / record.original_bytes) * 100))
		: 0;
	return `${bytes(record.saved_bytes)} saved${percent ? ` · ${percent}%` : ""}`;
}

function CapabilityTile({
	label,
	value,
	ready,
	detail,
}: {
	label: string;
	value: string;
	ready: boolean;
	detail: string;
}) {
	return (
		<div
			className={`rounded-2xl border p-4 ${
				ready ? "border-success/25 bg-success/5" : "border-warning/30 bg-warning/5"
			}`}
		>
			<div className="flex items-start justify-between gap-3">
				<div className="text-base-content/55 text-xs uppercase tracking-widest">{label}</div>
				{ready ? (
					<CheckCircle2 className="h-5 w-5 shrink-0 text-success" />
				) : (
					<AlertTriangle className="h-5 w-5 shrink-0 text-warning" />
				)}
			</div>
			<div className="mt-2 font-bold text-lg">{value}</div>
			<div className="mt-1 text-base-content/55 text-xs leading-relaxed">{detail}</div>
		</div>
	);
}

function SelectField({
	label,
	value,
	options,
	disabled,
	onChange,
}: {
	label: string;
	value: string;
	options: { value: string; label: string; disabled?: boolean }[];
	disabled?: boolean;
	onChange: (value: string) => void;
}) {
	return (
		<label className="form-control min-w-0">
			<span className="label-text mb-2 font-semibold">{label}</span>
			<select
				className="select select-bordered w-full bg-base-100"
				value={value}
				disabled={disabled}
				onChange={(event) => onChange(event.target.value)}
			>
				{options.map((option) => (
					<option key={option.value} value={option.value} disabled={option.disabled}>
						{option.label}
					</option>
				))}
			</select>
		</label>
	);
}

function ToggleRow({
	label,
	description,
	checked,
	disabled,
	onChange,
}: {
	label: string;
	description: string;
	checked: boolean;
	disabled?: boolean;
	onChange: (checked: boolean) => void;
}) {
	return (
		<label className="flex cursor-pointer items-start justify-between gap-4 rounded-xl border border-base-300 bg-base-200/30 p-4">
			<span>
				<span className="block font-semibold text-sm">{label}</span>
				<span className="mt-1 block text-base-content/55 text-xs leading-relaxed">
					{description}
				</span>
			</span>
			<input
				type="checkbox"
				className="toggle toggle-primary mt-1 shrink-0"
				checked={checked}
				disabled={disabled}
				onChange={(event) => onChange(event.target.checked)}
			/>
		</label>
	);
}

export function LibraryOptimizerConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: LibraryOptimizerConfigSectionProps) {
	const [data, setData] = useState(() => normalized(config.library_optimizer));
	const [hasChanges, setHasChanges] = useState(false);
	const [capabilities, setCapabilities] = useState<LibraryOptimizerCapabilities | null>(null);
	const [status, setStatus] = useState<LibraryOptimizerStatus | null>(null);
	const [analysis, setAnalysis] = useState<LibraryOptimizerAnalysis | null>(null);
	const [activeTab, setActiveTab] = useState<OptimizerTab>("setup");
	const [history, setHistory] = useState<LibraryOptimizerHistory | null>(null);
	const [historyStatus, setHistoryStatus] = useState("all");
	const [historySearchInput, setHistorySearchInput] = useState("");
	const [historySearch, setHistorySearch] = useState("");
	const [historyOffset, setHistoryOffset] = useState(0);
	const [isLoadingHistory, setIsLoadingHistory] = useState(false);
	const [isDetecting, setIsDetecting] = useState(false);
	const [isWorking, setIsWorking] = useState(false);
	const [error, setError] = useState("");
	const previousOptimizerRunning = useRef(false);

	useEffect(() => {
		setData(normalized(config.library_optimizer));
		setHasChanges(false);
	}, [config.library_optimizer]);

	const updateData = (next: LibraryOptimizerConfig) => {
		const nextNormalized = normalized(next);
		setData(nextNormalized);
		setAnalysis(null);
		setHasChanges(
			JSON.stringify(nextNormalized) !== JSON.stringify(normalized(config.library_optimizer)),
		);
	};

	const detect = useCallback(async () => {
		setIsDetecting(true);
		setError("");
		try {
			setCapabilities(await apiClient.detectLibraryOptimizerCapabilities());
		} catch (detectionError) {
			setError(
				detectionError instanceof Error ? detectionError.message : "Capability check failed",
			);
		} finally {
			setIsDetecting(false);
		}
	}, []);

	const refreshStatus = useCallback(async () => {
		try {
			const next = await apiClient.getLibraryOptimizerStatus();
			setStatus(next);
			if (next.analysis) setAnalysis(next.analysis);
		} catch {
			// The capability panel remains useful if status is unavailable during a restart.
		}
	}, []);

	const refreshHistory = useCallback(async () => {
		setIsLoadingHistory(true);
		setError("");
		try {
			setHistory(
				await apiClient.getLibraryOptimizerHistory({
					status: historyStatus,
					search: historySearch,
					offset: historyOffset,
					limit: HISTORY_PAGE_SIZE,
				}),
			);
		} catch (historyError) {
			setError(historyError instanceof Error ? historyError.message : "Optimizer history failed");
		} finally {
			setIsLoadingHistory(false);
		}
	}, [historyOffset, historySearch, historyStatus]);

	useEffect(() => {
		void detect();
		void refreshStatus();
	}, [detect, refreshStatus]);

	useEffect(() => {
		if (!status?.running) return;
		const timer = window.setInterval(() => void refreshStatus(), 2000);
		return () => window.clearInterval(timer);
	}, [status?.running, refreshStatus]);

	useEffect(() => {
		if (activeTab !== "activity" && activeTab !== "history") return;
		void refreshHistory();
	}, [activeTab, refreshHistory]);

	useEffect(() => {
		const running = status?.running === true;
		if (previousOptimizerRunning.current && !running && activeTab === "activity") {
			void refreshHistory();
		}
		previousOptimizerRunning.current = running;
	}, [activeTab, refreshHistory, status?.running]);

	const perform = async (action: () => Promise<LibraryOptimizerStatus>) => {
		setIsWorking(true);
		setError("");
		try {
			const next = await action();
			setStatus(next);
			if (next.analysis) setAnalysis(next.analysis);
		} catch (actionError) {
			setError(actionError instanceof Error ? actionError.message : "The optimizer request failed");
		} finally {
			setIsWorking(false);
		}
	};

	const analyze = async () => {
		setIsWorking(true);
		setError("");
		try {
			const next = await apiClient.analyzeLibraryOptimizer();
			setAnalysis(next);
			await refreshStatus();
		} catch (analysisError) {
			setError(analysisError instanceof Error ? analysisError.message : "Library analysis failed");
		} finally {
			setIsWorking(false);
		}
	};

	const save = async () => {
		if (!onUpdate || !hasChanges) return;
		await onUpdate("library_optimizer", data);
		setHasChanges(false);
		await detect();
	};

	const applyHistorySearch = () => {
		setHistoryOffset(0);
		setHistorySearch(historySearchInput.trim());
		if (historyOffset === 0 && historySearch === historySearchInput.trim()) {
			void refreshHistory();
		}
	};

	const hevcReady = capabilities?.hevc.some((option) => option.available) === true;
	const av1Ready = capabilities?.av1.some((option) => option.available) === true;
	const targetReady = data.target_codec === "av1" ? av1Ready : hevcReady;
	const systemReady =
		capabilities?.ffmpeg_available === true &&
		capabilities?.ffprobe_available === true &&
		capabilities?.library_writable === true &&
		targetReady;
	const startBlockedReason = (() => {
		if (isReadOnly) return "Configuration is read-only.";
		if (hasChanges) return "Save the optimization recipe before analyzing or starting.";
		if (!data.enabled) return "Enable Library Optimization under Setup, then save the recipe.";
		if (!capabilities) return "Waiting for the server capability check.";
		if (!capabilities.ffmpeg_available || !capabilities.ffprobe_available) {
			return "FFmpeg and FFprobe must be available.";
		}
		if (!capabilities.library_writable)
			return "Every enabled movie and series library must be writable.";
		if (!targetReady) return `No usable ${data.target_codec.toUpperCase()} encoder was detected.`;
		if (!analysis?.eligible_files)
			return "Analyze the library and confirm that at least one file is ready.";
		return "";
	})();
	const progress = status?.total
		? Math.min(100, Math.round((status.current / status.total) * 100))
		: 0;

	return (
		<div className="space-y-8">
			<div className="relative overflow-hidden rounded-3xl border border-primary/20 bg-gradient-to-br from-primary/15 via-base-200/80 to-secondary/10 p-6 shadow-sm">
				<div className="-right-10 -top-16 absolute h-56 w-56 rounded-full bg-primary/15 blur-3xl" />
				<div className="relative flex flex-col gap-5 sm:flex-row sm:items-center sm:justify-between">
					<div className="flex gap-4">
						<div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-2xl border border-primary/25 bg-primary/15 text-primary">
							<DatabaseZap className="h-7 w-7" />
						</div>
						<div>
							<div className="flex flex-wrap items-center gap-2">
								<h3 className="font-bold text-2xl">Tater Library Optimizer</h3>
								<span className="badge badge-primary badge-outline badge-sm">Local media</span>
							</div>
							<p className="mt-2 max-w-2xl text-base-content/65 text-sm leading-relaxed">
								Reclaim storage with HEVC or AV1 while keeping the best English main audio and the
								subtitles you choose. Every output is verified before the original moves.
							</p>
						</div>
					</div>
					<label className="flex cursor-pointer items-center gap-3 rounded-xl border border-base-300 bg-base-100/80 px-4 py-3 shadow-sm">
						<span className="font-semibold text-sm">{data.enabled ? "Enabled" : "Off"}</span>
						<input
							type="checkbox"
							className="toggle toggle-primary"
							checked={data.enabled}
							disabled={isReadOnly}
							onChange={(event) => updateData({ ...data, enabled: event.target.checked })}
						/>
					</label>
				</div>
			</div>

			<div
				role="tablist"
				className="tabs tabs-boxed w-fit max-w-full overflow-x-auto bg-base-200/70 p-1"
			>
				{[
					{ id: "setup" as const, label: "Setup", icon: Settings2 },
					{ id: "activity" as const, label: "Plan & Activity", icon: ListChecks },
					{ id: "history" as const, label: "History", icon: History },
				].map((tab) => {
					const Icon = tab.icon;
					return (
						<button
							key={tab.id}
							type="button"
							role="tab"
							aria-selected={activeTab === tab.id}
							className={`tab gap-2 whitespace-nowrap ${activeTab === tab.id ? "tab-active font-semibold" : ""}`}
							onClick={() => setActiveTab(tab.id)}
						>
							<Icon className="h-4 w-4" />
							{tab.label}
						</button>
					);
				})}
			</div>

			{activeTab === "setup" && (
				<div className="space-y-8">
					<section className="rounded-3xl border border-base-300 bg-base-200/25 p-5 sm:p-6">
						<div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
							<div>
								<div className="flex items-center gap-2">
									<Gauge className="h-5 w-5 text-primary" />
									<h3 className="font-bold text-xl">What this server can do</h3>
								</div>
								<p className="mt-1 text-base-content/55 text-sm">
									Detected capabilities show which encoders are ready on this server.
								</p>
							</div>
							<button
								type="button"
								className="btn btn-outline btn-sm"
								disabled={isDetecting}
								onClick={detect}
							>
								<RefreshCw className={`h-4 w-4 ${isDetecting ? "animate-spin" : ""}`} />
								{isDetecting ? "Checking…" : "Auto Detect"}
							</button>
						</div>

						{capabilities ? (
							<>
								<div className="mt-5 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
									<CapabilityTile
										label="Media tools"
										value={
											capabilities.ffmpeg_available && capabilities.ffprobe_available
												? "Ready"
												: "Unavailable"
										}
										ready={capabilities.ffmpeg_available && capabilities.ffprobe_available}
										detail={capabilities.ffmpeg_path || "FFmpeg and FFprobe are required"}
									/>
									<CapabilityTile
										label="HEVC"
										value={hevcReady ? "Ready" : "Unavailable"}
										ready={hevcReady}
										detail={
											capabilities.recommended_hevc
												? `Best encoder: ${capabilities.recommended_hevc}`
												: "No HEVC encoder passed detection"
										}
									/>
									<CapabilityTile
										label="AV1"
										value={av1Ready ? "Ready" : "Unavailable"}
										ready={av1Ready}
										detail={
											capabilities.recommended_av1
												? `Best encoder: ${capabilities.recommended_av1}`
												: "No AV1 encoder passed detection"
										}
									/>
									<CapabilityTile
										label="Library access"
										value={`${capabilities.writable_libraries}/${capabilities.total_libraries} writable`}
										ready={capabilities.library_writable}
										detail={
											capabilities.library_writable
												? "Safe replacement is available"
												: "Docker media mounts must be read/write"
										}
									/>
								</div>

								<div className="mt-5 grid gap-4 lg:grid-cols-2">
									{(["hevc", "av1"] as const).map((codec) => (
										<div
											key={codec}
											className="rounded-2xl border border-base-300 bg-base-100/60 p-4"
										>
											<div className="mb-3 flex items-center justify-between">
												<div className="font-bold">{codec.toUpperCase()} encoders</div>
												<span className="badge badge-ghost badge-sm">
													{capabilities[codec].filter((option) => option.available).length} ready
												</span>
											</div>
											<div className="space-y-2">
												{capabilities[codec].map((option) => (
													<div
														key={option.encoder}
														className="flex items-start justify-between gap-3 text-sm"
													>
														<div className="min-w-0">
															<div className="font-semibold">{option.label}</div>
															{option.available && (
																<div className="break-words text-base-content/50 text-xs">
																	{option.details || option.status}
																</div>
															)}
														</div>
														<span
															className={`badge badge-sm shrink-0 ${option.available ? "badge-success badge-outline" : "badge-ghost"}`}
														>
															{option.available ? "Ready" : "Unavailable"}
														</span>
													</div>
												))}
											</div>
										</div>
									))}
								</div>

								<div className="mt-4 flex flex-wrap gap-2">
									<span
										className={`badge badge-lg ${capabilities.audio_copy ? "badge-success badge-outline" : "badge-ghost"}`}
									>
										<Volume2 className="mr-1 h-4 w-4" /> Audio copy
									</span>
									<span
										className={`badge badge-lg ${capabilities.hdr_preservation ? "badge-success badge-outline" : "badge-ghost"}`}
									>
										<Sparkles className="mr-1 h-4 w-4" /> HDR preserved automatically
									</span>
									<span
										className={`badge badge-lg ${capabilities.dolby_vision_hdr10 ? "badge-success badge-outline" : "badge-ghost"}`}
									>
										{capabilities.dolby_vision_policy}
									</span>
								</div>
								{capabilities.notes && capabilities.notes.length > 0 && (
									<div className="mt-4 rounded-xl border border-info/20 bg-info/5 p-4 text-base-content/65 text-xs leading-relaxed">
										{capabilities.notes.map((note) => (
											<div key={note} className="mb-1 last:mb-0">
												• {note}
											</div>
										))}
									</div>
								)}
							</>
						) : (
							<div className="mt-5 flex items-center gap-3 rounded-xl border border-base-300 p-4 text-base-content/55 text-sm">
								<span className="loading loading-spinner loading-sm" /> Checking the server and
								library mounts…
							</div>
						)}
					</section>

					<section className="space-y-5">
						<div>
							<h3 className="font-bold text-xl">Optimization recipe</h3>
							<p className="text-base-content/55 text-sm">
								Choose the storage target and what the finished media should retain.
							</p>
						</div>
						<div className="grid gap-3 md:grid-cols-2">
							{[
								{
									id: "hevc",
									label: "HEVC",
									badge: "Recommended",
									detail:
										"Fast, widely supported, and a strong storage upgrade for H.264 libraries.",
									ready: hevcReady,
								},
								{
									id: "av1",
									label: "AV1",
									badge: "Maximum savings",
									detail:
										"Newer compression with smaller files, but slower encoding and newer-player requirements.",
									ready: av1Ready,
								},
							].map((option) => {
								const selected = data.target_codec === option.id;
								return (
									<button
										key={option.id}
										type="button"
										disabled={isReadOnly || (capabilities !== null && !option.ready)}
										onClick={() => updateData({ ...data, target_codec: option.id })}
										className={`rounded-2xl border p-5 text-left transition ${selected ? "border-primary bg-primary/10 shadow-[0_0_0_1px_hsl(var(--p)/0.2)]" : "border-base-300 bg-base-200/30 hover:border-primary/35"} disabled:cursor-not-allowed disabled:opacity-45`}
									>
										<div className="flex items-center justify-between gap-3">
											<div className="font-bold text-xl">{option.label}</div>
											<span className="badge badge-ghost badge-sm">
												{option.ready ? option.badge : "Unavailable"}
											</span>
										</div>
										<p className="mt-2 text-base-content/60 text-sm leading-relaxed">
											{option.detail}
										</p>
									</button>
								);
							})}
						</div>
						<div className="flex items-start gap-2 rounded-xl border border-info/20 bg-info/5 px-4 py-3 text-base-content/65 text-sm">
							<Info className="mt-0.5 h-4 w-4 shrink-0 text-info" />
							<span>
								Video handling is automatic. AV1 converts HEVC sources; video that already meets the
								selected target is copied unchanged while the audio and subtitle policy is applied.
							</span>
						</div>

						<div className="grid gap-4 md:grid-cols-2">
							<SelectField
								label="Quality"
								value={data.quality}
								disabled={isReadOnly}
								onChange={(quality) => updateData({ ...data, quality })}
								options={[
									{ value: "high", label: "High quality · larger output" },
									{ value: "balanced", label: "Balanced · recommended" },
									{ value: "small", label: "Smaller files" },
								]}
							/>
							<SelectField
								label="Audio tracks"
								value={data.audio_policy}
								disabled={isReadOnly}
								onChange={(audio_policy) => updateData({ ...data, audio_policy })}
								options={[
									{ value: "best_english", label: "Best English main track" },
									{ value: "all_english", label: "All English main tracks" },
									{ value: "all", label: "All main tracks" },
								]}
							/>
							<SelectField
								label="Audio format"
								value={data.audio_format}
								disabled={isReadOnly}
								onChange={(audio_format) => updateData({ ...data, audio_format })}
								options={[
									{ value: "copy", label: "Keep original quality" },
									{
										value: "eac3",
										label:
											capabilities?.eac3 === false ? "E-AC-3 · unavailable" : "Convert to E-AC-3",
										disabled: capabilities?.eac3 === false,
									},
									{
										value: "aac",
										label: capabilities?.aac === false ? "AAC · unavailable" : "Convert to AAC",
										disabled: capabilities?.aac === false,
									},
								]}
							/>
							<SelectField
								label="Subtitles"
								value={data.subtitle_policy}
								disabled={isReadOnly}
								onChange={(subtitle_policy) => updateData({ ...data, subtitle_policy })}
								options={[
									{ value: "forced_english", label: "Forced English only · recommended" },
									{ value: "english", label: "All English subtitles" },
									{ value: "remove", label: "Remove all subtitles" },
									{ value: "all", label: "Keep all subtitles" },
								]}
							/>
						</div>

						<div className="grid gap-3 lg:grid-cols-2">
							<ToggleRow
								label="Pause for playback"
								description="Finish the current FFmpeg step, then wait while anyone is streaming."
								checked={data.pause_during_playback}
								disabled={isReadOnly}
								onChange={(pause_during_playback) => updateData({ ...data, pause_during_playback })}
							/>
							<ToggleRow
								label="Protect Dolby Vision"
								description={
									data.skip_dolby_vision
										? "Always skip Dolby Vision video and leave its dynamic metadata untouched."
										: "Preserve efficient Dolby Vision video. Convert it to verified 10-bit HDR10 only when the selected codec requires a video transcode."
								}
								checked={data.skip_dolby_vision}
								disabled={isReadOnly}
								onChange={(skip_dolby_vision) => updateData({ ...data, skip_dolby_vision })}
							/>
							<ToggleRow
								label="Automatic optimization"
								description="Periodically process new eligible local media automatically."
								checked={data.automatic}
								disabled={isReadOnly || !data.enabled}
								onChange={(automatic) => updateData({ ...data, automatic })}
							/>
						</div>

						<div className="grid gap-4 md:grid-cols-2">
							<label className="form-control">
								<span className="label-text mb-2 font-semibold">Minimum space savings</span>
								<div className="join w-full">
									<input
										type="number"
										min={0}
										max={90}
										className="input input-bordered join-item w-full"
										value={data.minimum_savings_percent}
										disabled={isReadOnly}
										onChange={(event) =>
											updateData({ ...data, minimum_savings_percent: Number(event.target.value) })
										}
									/>
									<span className="join-item flex items-center border border-base-300 bg-base-200 px-4">
										%
									</span>
								</div>
								<span className="mt-1 text-base-content/50 text-xs">
									Discard an output that does not save at least this much.
								</span>
							</label>
							<label className="form-control">
								<span className="label-text mb-2 font-semibold">Original-file safety window</span>
								<div className="join w-full">
									<input
										type="number"
										min={0}
										max={365}
										className="input input-bordered join-item w-full"
										value={data.quarantine_days}
										disabled={isReadOnly}
										onChange={(event) =>
											updateData({ ...data, quarantine_days: Number(event.target.value) })
										}
									/>
									<span className="join-item flex items-center border border-base-300 bg-base-200 px-4">
										days
									</span>
								</div>
								<span className="mt-1 text-base-content/50 text-xs">
									Hidden originals stay beside the media before automatic cleanup. Use 0 to remove
									after verification.
								</span>
							</label>
						</div>
					</section>

					<div className="flex justify-end">
						<button
							type="button"
							className="btn btn-primary min-w-40"
							disabled={isReadOnly || isUpdating || !hasChanges}
							onClick={save}
						>
							{isUpdating ? (
								<span className="loading loading-spinner loading-sm" />
							) : (
								<Save className="h-4 w-4" />
							)}
							Save recipe
						</button>
					</div>
				</div>
			)}

			{activeTab === "activity" && (
				<section className="rounded-3xl border border-base-300 bg-base-200/25 p-5 sm:p-6">
					<div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
						<div>
							<div className="flex items-center gap-2">
								<HardDrive className="h-5 w-5 text-primary" />
								<h3 className="font-bold text-xl">Library plan & activity</h3>
							</div>
							<p className="mt-1 text-base-content/55 text-sm">
								Analyze first to see what will change. Analysis never modifies a file.
							</p>
						</div>
						<div className="flex flex-wrap gap-2">
							<button
								type="button"
								className="btn btn-outline btn-sm"
								disabled={isWorking || status?.running || hasChanges}
								title={hasChanges ? "Save the optimization recipe before analyzing." : undefined}
								onClick={analyze}
							>
								<RefreshCw className={`h-4 w-4 ${isWorking ? "animate-spin" : ""}`} />
								Analyze
							</button>
							{status?.running ? (
								<>
									<button
										type="button"
										className="btn btn-outline btn-sm"
										disabled={isWorking}
										onClick={() =>
											perform(() => apiClient.setLibraryOptimizerPaused(!status.paused))
										}
									>
										{status.paused ? <Play className="h-4 w-4" /> : <Pause className="h-4 w-4" />}
										{status.paused ? "Resume" : "Pause"}
									</button>
									<button
										type="button"
										className="btn btn-error btn-outline btn-sm"
										disabled={isWorking}
										onClick={() => perform(() => apiClient.cancelLibraryOptimizer())}
									>
										<Square className="h-4 w-4" />
										Stop
									</button>
								</>
							) : (
								<button
									type="button"
									className="btn btn-primary btn-sm"
									disabled={
										isWorking ||
										isReadOnly ||
										hasChanges ||
										!data.enabled ||
										!systemReady ||
										!analysis?.eligible_files
									}
									title={startBlockedReason || undefined}
									onClick={() => perform(() => apiClient.startLibraryOptimizer())}
								>
									<Play className="h-4 w-4" />
									Start optimization
								</button>
							)}
						</div>
					</div>

					{!status?.running && startBlockedReason && (
						<div className="mt-4 flex items-start gap-2 rounded-xl border border-info/20 bg-info/5 px-4 py-3 text-base-content/65 text-sm">
							<Info className="mt-0.5 h-4 w-4 shrink-0 text-info" />
							<span>{startBlockedReason}</span>
						</div>
					)}

					{analysis && (
						<>
							<div className="mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
								<CapabilityTile
									label="Scanned"
									value={`${analysis.total_files} files`}
									ready={true}
									detail={bytes(analysis.source_bytes)}
								/>
								<CapabilityTile
									label="Ready"
									value={`${analysis.eligible_files} files`}
									ready={analysis.eligible_files > 0}
									detail={`${analysis.video_conversions} transcodes · ${analysis.track_cleanup_only} cleanup`}
								/>
								<CapabilityTile
									label="Previously handled"
									value={`${analysis.previously_handled} files`}
									ready={true}
									detail="No repeat work needed"
								/>
								<CapabilityTile
									label="Eligible size"
									value={bytes(analysis.eligible_bytes)}
									ready={analysis.eligible_files > 0}
									detail={`${analysis.dolby_vision_skipped} Dolby Vision preserved · ${analysis.dolby_vision_hdr10} need HDR10 conversion`}
								/>
							</div>
							{Object.keys(analysis.codecs).length > 0 && (
								<div className="mt-3 flex flex-wrap items-center gap-2 rounded-xl border border-base-300 bg-base-100/45 px-4 py-3">
									<span className="mr-1 font-semibold text-base-content/55 text-xs">
										Detected video
									</span>
									{Object.entries(analysis.codecs)
										.sort((left, right) => right[1] - left[1])
										.map(([codec, count]) => (
											<span
												key={codec}
												className="badge badge-outline badge-sm font-mono uppercase"
											>
												{codec} · {count.toLocaleString()}
											</span>
										))}
								</div>
							)}
						</>
					)}

					{analysis && (analysis.media_details_missing > 0 || analysis.unavailable_files > 0) && (
						<div className="alert alert-warning mt-4 items-start py-3 text-sm">
							<AlertTriangle className="mt-0.5 h-4 w-4" />
							<div>
								<div className="font-semibold">Some files were excluded for safety</div>
								<div className="mt-1 text-xs opacity-75">
									{analysis.media_details_missing > 0 && (
										<span>
											{analysis.media_details_missing.toLocaleString()} need codec details. Run Scan
											Libraries, then analyze again.
										</span>
									)}
									{analysis.media_details_missing > 0 && analysis.unavailable_files > 0 && " · "}
									{analysis.unavailable_files > 0 && (
										<span>
											{analysis.unavailable_files.toLocaleString()} files are unavailable.
										</span>
									)}
								</div>
							</div>
						</div>
					)}

					{status && (
						<>
							{history && (
								<div className="mt-5 overflow-hidden rounded-2xl border border-primary/25 bg-primary/5">
									<div className="flex flex-col gap-4 p-4 sm:flex-row sm:items-center sm:justify-between sm:p-5">
										<div className="flex items-center gap-3">
											<div className="rounded-xl bg-primary/15 p-2.5 text-primary">
												<ArchiveRestore className="h-5 w-5" />
											</div>
											<div>
												<div className="font-bold text-base">All-time library savings</div>
												<div className="mt-0.5 text-base-content/55 text-xs">
													Persistent across optimization runs and server updates
												</div>
											</div>
										</div>
										<div className="sm:text-right">
											<div className="font-black text-2xl text-primary">
												{bytes(history.lifetime.saved_bytes)}
											</div>
											<div className="mt-0.5 text-base-content/60 text-xs">
												across {history.lifetime.completed_files.toLocaleString()} completed files
											</div>
										</div>
									</div>
								</div>
							)}
							<div className="mt-5 rounded-2xl border border-base-300 bg-base-100/55 p-4">
								<div className="flex flex-wrap items-center justify-between gap-2">
									<div className="flex items-center gap-2">
										{status.running ? (
											<span className="loading loading-spinner loading-sm text-primary" />
										) : (
											<CheckCircle2 className="h-5 w-5 text-success" />
										)}
										<span className="font-semibold">{status.message || status.state}</span>
									</div>
									{status.waiting_for_playback && (
										<span className="badge badge-warning badge-outline">
											<Clock3 className="mr-1 h-3.5 w-3.5" />
											Waiting for playback
										</span>
									)}
									{status.waiting_for_media_markers && (
										<span className="badge badge-warning badge-outline">
											<Clock3 className="mr-1 h-3.5 w-3.5" />
											Waiting for Breaks & Credits
										</span>
									)}
								</div>
								{status.running && (
									<>
										<progress
											className="progress progress-primary mt-4 w-full"
											value={progress}
											max="100"
										/>
										<div className="mt-2 flex justify-between gap-3 text-base-content/55 text-xs">
											<span className="truncate">{status.current_file || "Preparing…"}</span>
											<span className="shrink-0">
												{status.current}/{status.total}
											</span>
										</div>
									</>
								)}
								<div className="mt-4 flex flex-wrap gap-x-5 gap-y-1 text-base-content/60 text-xs">
									<span>{status.completed} completed</span>
									<span>{status.skipped} skipped</span>
									<span>{status.failed} failed</span>
									<span>{bytes(status.saved_bytes)} saved by completed files in this run</span>
								</div>
								{status.last_error && (
									<div className="mt-3 break-words rounded-lg bg-error/10 p-3 text-error text-xs">
										{status.last_error}
									</div>
								)}
							</div>
						</>
					)}
				</section>
			)}

			{activeTab === "history" && (
				<section className="rounded-3xl border border-base-300 bg-base-200/25 p-5 sm:p-6">
					<div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
						<div>
							<div className="flex items-center gap-2">
								<History className="h-5 w-5 text-primary" />
								<h3 className="font-bold text-xl">Optimization history</h3>
							</div>
							<p className="mt-1 text-base-content/55 text-sm">
								The latest persistent result for every processed file, kept across runs and server
								updates.
							</p>
						</div>
						<button
							type="button"
							className="btn btn-outline btn-sm w-fit"
							disabled={isLoadingHistory}
							onClick={() => void refreshHistory()}
						>
							<RefreshCw className={`h-4 w-4 ${isLoadingHistory ? "animate-spin" : ""}`} />
							Refresh
						</button>
					</div>

					{history && (
						<div className="mt-5 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
							<CapabilityTile
								label="Processed media"
								value={`${history.summary.total} files`}
								ready={true}
								detail={`${history.summary.completed} successful · ${history.summary.skipped} skipped`}
							/>
							<CapabilityTile
								label="Successful"
								value={`${history.summary.completed} files`}
								ready={true}
								detail={`${bytes(history.summary.original_bytes)} → ${bytes(history.summary.output_bytes)}`}
							/>
							<CapabilityTile
								label="Failed"
								value={`${history.summary.failed} files`}
								ready={history.summary.failed === 0}
								detail={history.summary.failed ? "Review the results below" : "No failed media"}
							/>
							<CapabilityTile
								label="All-time space saved"
								value={bytes(history.lifetime.saved_bytes)}
								ready={true}
								detail={`${history.lifetime.completed_files.toLocaleString()} completed files · never resets`}
							/>
						</div>
					)}

					<div className="mt-5 flex flex-col gap-3 md:flex-row">
						<form
							className="join w-full md:max-w-xl"
							onSubmit={(event) => {
								event.preventDefault();
								applyHistorySearch();
							}}
						>
							<label className="input input-bordered join-item flex w-full items-center gap-2 bg-base-100">
								<Search className="h-4 w-4 shrink-0 text-base-content/45" />
								<input
									type="search"
									className="min-w-0 grow"
									placeholder="Search media or error details"
									value={historySearchInput}
									onChange={(event) => setHistorySearchInput(event.target.value)}
								/>
							</label>
							<button type="submit" className="btn btn-primary join-item">
								Search
							</button>
						</form>
						<select
							className="select select-bordered bg-base-100 md:w-48"
							value={historyStatus}
							onChange={(event) => {
								setHistoryStatus(event.target.value);
								setHistoryOffset(0);
							}}
						>
							<option value="all">All results</option>
							<option value="completed">Successful</option>
							<option value="skipped">Skipped</option>
							<option value="failed">Failed</option>
						</select>
					</div>

					<div className="mt-4 overflow-hidden rounded-2xl border border-base-300 bg-base-100/55">
						{isLoadingHistory && !history ? (
							<div className="flex items-center justify-center gap-3 p-10 text-base-content/55 text-sm">
								<span className="loading loading-spinner loading-sm" /> Loading optimizer history…
							</div>
						) : history?.records.length ? (
							<div className="divide-y divide-base-300">
								{history.records.map((record) => (
									<div key={`${record.path}-${record.updated_at}`} className="p-4 sm:p-5">
										<div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
											<div className="min-w-0">
												<div className="break-words font-semibold">{mediaName(record.path)}</div>
												<div className="mt-1 break-all text-base-content/45 text-xs">
													{record.path}
												</div>
											</div>
											<span
												className={`badge badge-sm shrink-0 capitalize ${historyStatusClass(record.status)}`}
											>
												{record.status === "completed" ? "Successful" : record.status}
											</span>
										</div>
										<div className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-base-content/60 text-xs">
											{record.source_codec && (
												<span>
													{record.source_codec.toUpperCase()} →{" "}
													{record.target_codec?.toUpperCase() || "COPY"}
												</span>
											)}
											{record.original_bytes ? (
												<span>{bytes(record.original_bytes)} original</span>
											) : null}
											{record.output_bytes ? (
												<span>{bytes(record.output_bytes)} output</span>
											) : null}
											{historySavings(record) && (
												<span className="font-semibold text-success">{historySavings(record)}</span>
											)}
											<span>{new Date(record.updated_at).toLocaleString()}</span>
										</div>
										{record.message && (
											<div
												className={`mt-3 text-xs ${record.status === "failed" ? "text-error" : "text-base-content/55"}`}
											>
												{record.message}
											</div>
										)}
									</div>
								))}
							</div>
						) : (
							<div className="p-10 text-center text-base-content/55 text-sm">
								No optimizer results match this view yet.
							</div>
						)}
					</div>

					{history && history.filtered_total > 0 && (
						<div className="mt-4 flex flex-wrap items-center justify-between gap-3 text-base-content/55 text-xs">
							<span>
								Showing {history.offset + 1}–
								{Math.min(history.offset + history.records.length, history.filtered_total)} of{" "}
								{history.filtered_total}
							</span>
							<div className="join">
								<button
									type="button"
									className="btn btn-outline btn-sm join-item"
									disabled={history.offset === 0 || isLoadingHistory}
									onClick={() => setHistoryOffset(Math.max(0, history.offset - HISTORY_PAGE_SIZE))}
								>
									<ChevronLeft className="h-4 w-4" /> Previous
								</button>
								<button
									type="button"
									className="btn btn-outline btn-sm join-item"
									disabled={
										history.offset + history.records.length >= history.filtered_total ||
										isLoadingHistory
									}
									onClick={() => setHistoryOffset(history.offset + HISTORY_PAGE_SIZE)}
								>
									Next <ChevronRight className="h-4 w-4" />
								</button>
							</div>
						</div>
					)}
				</section>
			)}

			{activeTab === "setup" && (
				<div className="grid gap-3 md:grid-cols-2">
					<div className="flex gap-3 rounded-2xl border border-success/25 bg-success/5 p-4 text-sm">
						<ShieldCheck className="mt-0.5 h-5 w-5 shrink-0 text-success" />
						<div>
							<div className="font-semibold">Verified replacement</div>
							<p className="mt-1 text-base-content/60 text-xs leading-relaxed">
								The new file must probe correctly, retain video and audio, match duration, and meet
								your savings target before it replaces the source.
							</p>
						</div>
					</div>
					<div className="flex gap-3 rounded-2xl border border-info/25 bg-info/5 p-4 text-sm">
						<ArchiveRestore className="mt-0.5 h-5 w-5 shrink-0 text-info" />
						<div>
							<div className="font-semibold">Storage stays with the library</div>
							<p className="mt-1 text-base-content/60 text-xs leading-relaxed">
								Temporary output and hidden quarantine copies live beside the source.{" "}
								<code>/config</code> stores only compact optimizer history.
							</p>
						</div>
					</div>
				</div>
			)}

			{error && (
				<div className="flex gap-3 rounded-xl border border-error/30 bg-error/10 p-4 text-error text-sm">
					<AlertTriangle className="h-5 w-5 shrink-0" />
					<span className="break-words">{error}</span>
				</div>
			)}
			{hasChanges && (
				<div className="flex gap-3 rounded-xl border border-warning/25 bg-warning/5 p-4 text-sm">
					<Info className="h-5 w-5 shrink-0 text-warning" />
					<span>
						Save the recipe before analyzing or starting so the server uses these choices.
					</span>
				</div>
			)}
		</div>
	);
}
