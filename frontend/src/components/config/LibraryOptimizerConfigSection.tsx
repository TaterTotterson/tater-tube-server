import {
	AlertTriangle,
	ArchiveRestore,
	CheckCircle2,
	Clock3,
	DatabaseZap,
	Gauge,
	HardDrive,
	Info,
	Pause,
	Play,
	RefreshCw,
	Save,
	ShieldCheck,
	Sparkles,
	Square,
	Volume2,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import type {
	ConfigResponse,
	LibraryOptimizerAnalysis,
	LibraryOptimizerCapabilities,
	LibraryOptimizerConfig,
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
	convert_hevc_to_av1: false,
	clean_efficient_files: false,
	pause_during_playback: true,
	preserve_hdr: true,
	skip_dolby_vision: true,
	minimum_savings_percent: 15,
	quarantine_days: 7,
};

function normalized(value?: LibraryOptimizerConfig): LibraryOptimizerConfig {
	return { ...DEFAULTS, ...value };
}

function bytes(value = 0) {
	if (!Number.isFinite(value) || value <= 0) return "0 B";
	const units = ["B", "KB", "MB", "GB", "TB"];
	const power = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
	return `${(value / 1024 ** power).toFixed(power > 2 ? 2 : 1)} ${units[power]}`;
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
	const [isDetecting, setIsDetecting] = useState(false);
	const [isWorking, setIsWorking] = useState(false);
	const [error, setError] = useState("");

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

	useEffect(() => {
		void detect();
		void refreshStatus();
	}, [detect, refreshStatus]);

	useEffect(() => {
		if (!status?.running) return;
		const timer = window.setInterval(() => void refreshStatus(), 2000);
		return () => window.clearInterval(timer);
	}, [status?.running, refreshStatus]);

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

	const hevcReady = capabilities?.hevc.some((option) => option.available) === true;
	const av1Ready = capabilities?.av1.some((option) => option.available) === true;
	const targetReady = data.target_codec === "av1" ? av1Ready : hevcReady;
	const systemReady =
		capabilities?.ffmpeg_available === true &&
		capabilities?.ffprobe_available === true &&
		capabilities?.library_writable === true &&
		targetReady;
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

			<section className="rounded-3xl border border-base-300 bg-base-200/25 p-5 sm:p-6">
				<div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
					<div>
						<div className="flex items-center gap-2">
							<Gauge className="h-5 w-5 text-primary" />
							<h3 className="font-bold text-xl">What this server can do</h3>
						</div>
						<p className="mt-1 text-base-content/55 text-sm">
							Detected capabilities explain which choices are ready and why others are unavailable.
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
								<div key={codec} className="rounded-2xl border border-base-300 bg-base-100/60 p-4">
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
													<div className="break-words text-base-content/50 text-xs">
														{option.details || option.status}
													</div>
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
								<Sparkles className="mr-1 h-4 w-4" /> HDR preservation
							</span>
							<span className="badge badge-ghost badge-lg">{capabilities.dolby_vision_policy}</span>
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
						<span className="loading loading-spinner loading-sm" /> Checking the server and library
						mounts…
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
							detail: "Fast, widely supported, and a strong storage upgrade for H.264 libraries.",
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
								<p className="mt-2 text-base-content/60 text-sm leading-relaxed">{option.detail}</p>
							</button>
						);
					})}
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
								label: capabilities?.eac3 === false ? "E-AC-3 · unavailable" : "Convert to E-AC-3",
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
						label="Preserve HDR"
						description="Keep HDR10 signaling and 10-bit video when the chosen encoder supports it."
						checked={data.preserve_hdr}
						disabled={isReadOnly}
						onChange={(preserve_hdr) => updateData({ ...data, preserve_hdr })}
					/>
					<ToggleRow
						label="Protect Dolby Vision"
						description="Skip Dolby Vision sources instead of risking loss of dynamic metadata."
						checked={data.skip_dolby_vision}
						disabled={isReadOnly}
						onChange={(skip_dolby_vision) => updateData({ ...data, skip_dolby_vision })}
					/>
					<ToggleRow
						label="Automatic optimization"
						description="Periodically process new eligible local media when playback is idle."
						checked={data.automatic}
						disabled={isReadOnly || !data.enabled}
						onChange={(automatic) => updateData({ ...data, automatic })}
					/>
					<ToggleRow
						label="Convert HEVC to AV1"
						description="Normally HEVC is already efficient and skipped. Enable only when AV1 is selected."
						checked={data.convert_hevc_to_av1}
						disabled={isReadOnly || data.target_codec !== "av1"}
						onChange={(convert_hevc_to_av1) => updateData({ ...data, convert_hevc_to_av1 })}
					/>
					<ToggleRow
						label="Clean tracks in efficient files"
						description="Remux existing HEVC/AV1 files to apply the audio and subtitle policy without re-encoding video."
						checked={data.clean_efficient_files}
						disabled={isReadOnly}
						onChange={(clean_efficient_files) => updateData({ ...data, clean_efficient_files })}
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
							Hidden originals stay beside the media before automatic cleanup. Use 0 to remove after
							verification.
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
							disabled={isWorking || status?.running}
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
									onClick={() => perform(() => apiClient.setLibraryOptimizerPaused(!status.paused))}
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
								onClick={() => perform(() => apiClient.startLibraryOptimizer())}
							>
								<Play className="h-4 w-4" />
								Start optimization
							</button>
						)}
					</div>
				</div>

				{analysis && (
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
							label="Already efficient"
							value={`${analysis.already_efficient} files`}
							ready={true}
							detail={`${analysis.previously_handled} previously handled`}
						/>
						<CapabilityTile
							label="Eligible size"
							value={bytes(analysis.eligible_bytes)}
							ready={analysis.eligible_files > 0}
							detail={`${analysis.dolby_vision_skipped} Dolby Vision protected`}
						/>
					</div>
				)}

				{status && (
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
							<span>{bytes(status.saved_bytes)} saved after the safety window</span>
						</div>
						{status.last_error && (
							<div className="mt-3 break-words rounded-lg bg-error/10 p-3 text-error text-xs">
								{status.last_error}
							</div>
						)}
					</div>
				)}
			</section>

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
