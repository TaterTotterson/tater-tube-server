import {
	CircleCheck,
	Clock3,
	FileCheck2,
	Film,
	ListTodo,
	RefreshCw,
	Save,
	ScanSearch,
	Sparkles,
	TriangleAlert,
} from "lucide-react";
import { useEffect, useState } from "react";
import { useMediaMarkerStatus, useScanMediaMarkers } from "../../hooks/useApi";
import type { ConfigResponse, MediaMarkersConfig } from "../../types/config";

interface MediaMarkersConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: MediaMarkersConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

function normalize(value?: MediaMarkersConfig): MediaMarkersConfig {
	return { enabled: value?.enabled !== false };
}

function StatusTile({
	label,
	value,
	detail,
	icon: Icon,
	tone = "primary",
}: {
	label: string;
	value: string;
	detail: string;
	icon: typeof CircleCheck;
	tone?: "primary" | "success" | "warning";
}) {
	const toneClass = {
		primary: "border-primary/20 bg-primary/5 text-primary",
		success: "border-success/20 bg-success/5 text-success",
		warning: "border-warning/20 bg-warning/5 text-warning",
	}[tone];
	return (
		<div className={`rounded-2xl border p-4 ${toneClass}`}>
			<div className="flex items-center justify-between gap-3">
				<span className="font-bold text-current/75 text-xs uppercase tracking-wider">{label}</span>
				<Icon className="h-4 w-4" />
			</div>
			<div className="mt-2 font-black text-2xl text-base-content">{value}</div>
			<div className="mt-1 text-base-content/50 text-xs">{detail}</div>
		</div>
	);
}

export function MediaMarkersConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: MediaMarkersConfigSectionProps) {
	const [data, setData] = useState(() => normalize(config.media_markers));
	const [hasChanges, setHasChanges] = useState(false);
	const { data: status } = useMediaMarkerStatus();
	const scan = useScanMediaMarkers();

	useEffect(() => {
		setData(normalize(config.media_markers));
		setHasChanges(false);
	}, [config.media_markers]);

	const updateEnabled = (enabled: boolean) => {
		setData({ enabled });
		setHasChanges(enabled !== normalize(config.media_markers).enabled);
	};

	const save = async () => {
		if (!onUpdate || !hasChanges) return;
		await onUpdate("media_markers", data);
		setHasChanges(false);
	};

	const progressTotal = (status?.checked ?? 0) + (status?.queued ?? 0);
	const progress =
		progressTotal > 0 ? Math.round(((status?.checked ?? 0) / progressTotal) * 100) : 0;

	return (
		<div className="space-y-6">
			<section className="relative overflow-hidden rounded-2xl border border-primary/25 bg-gradient-to-br from-primary/12 via-base-200/70 to-warning/5 p-5 sm:p-6">
				<div className="-right-16 -top-20 absolute h-56 w-56 rounded-full bg-primary/15 blur-3xl" />
				<div className="relative flex flex-col gap-5 sm:flex-row sm:items-center sm:justify-between">
					<div className="flex min-w-0 gap-4">
						<div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl border border-primary/25 bg-primary/10 text-primary">
							<ScanSearch className="h-6 w-6" />
						</div>
						<div>
							<h3 className="font-bold text-xl">Automatic media markers</h3>
							<p className="mt-2 max-w-2xl text-base-content/65 text-sm leading-relaxed">
								Analyze new or changed local videos for natural commercial transitions and ending
								credits. Results are saved beside the media and reused permanently.
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
							onChange={(event) => updateEnabled(event.target.checked)}
						/>
					</label>
				</div>
			</section>

			<section className="rounded-2xl border border-base-300 bg-base-100/60 p-5">
				<div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
					<div>
						<div className="flex flex-wrap items-center gap-2">
							<h3 className="font-bold text-lg">Library marker status</h3>
							<span
								className={`badge badge-sm ${status?.running ? "badge-primary" : status?.failed ? "badge-warning" : data.enabled ? "badge-success" : "badge-ghost"}`}
							>
								{status?.running ? "Scanning" : data.enabled ? "Ready" : "Automatic scan off"}
							</span>
						</div>
						<p className="mt-1 text-base-content/55 text-sm">
							{status?.message || "Waiting for marker status"}
						</p>
					</div>
					<button
						type="button"
						className="btn btn-outline btn-sm shrink-0"
						disabled={
							isReadOnly || scan.isPending || status?.running || !config.local_media.enabled
						}
						onClick={() => scan.mutate()}
					>
						<RefreshCw className={`h-4 w-4 ${status?.running ? "animate-spin" : ""}`} />
						Scan missing now
					</button>
				</div>

				<div className="mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
					<StatusTile
						label="Completed"
						value={(status?.completed ?? 0).toLocaleString()}
						detail={`${(status?.library_videos ?? 0).toLocaleString()} local videos`}
						icon={CircleCheck}
						tone="success"
					/>
					<StatusTile
						label="In queue"
						value={(status?.queued ?? 0).toLocaleString()}
						detail={`${(status?.checked ?? 0).toLocaleString()} checked this run`}
						icon={ListTodo}
					/>
					<StatusTile
						label="Breaks found"
						value={(status?.commercial_breaks ?? 0).toLocaleString()}
						detail={`${(status?.credits ?? 0).toLocaleString()} credits markers`}
						icon={Sparkles}
					/>
					<StatusTile
						label="Skipped"
						value={(status?.failed ?? 0).toLocaleString()}
						detail={`${(status?.created ?? 0).toLocaleString()} new · ${(status?.reused ?? 0).toLocaleString()} reused`}
						icon={status?.failed ? TriangleAlert : FileCheck2}
						tone={status?.failed ? "warning" : "success"}
					/>
				</div>

				{status?.running && (
					<div className="mt-5 rounded-xl border border-primary/20 bg-primary/5 p-4">
						<div className="flex items-center justify-between gap-3 text-sm">
							<span className="min-w-0 truncate font-semibold">
								{status.current_file || "Analyzing queued video"}
							</span>
							<span className="shrink-0 font-bold text-primary">{progress}%</span>
						</div>
						<progress
							className="progress progress-primary mt-3 h-2 w-full"
							value={progress}
							max={100}
						/>
					</div>
				)}

				{(status?.failed ?? 0) > 0 && (
					<div className="alert alert-warning mt-5 items-start text-sm">
						<TriangleAlert className="mt-0.5 h-4 w-4" />
						<div className="min-w-0">
							<div className="font-bold">Some videos could not be scanned</div>
							<div className="mt-1 break-all text-xs opacity-75">
								{status?.errors?.[0] || "Check that the media folder is writable."}
							</div>
						</div>
					</div>
				)}

				{status?.finished_at && (
					<div className="mt-4 flex items-center gap-2 text-base-content/45 text-xs">
						<Clock3 className="h-3.5 w-3.5" />
						Last completed {new Date(status.finished_at).toLocaleString()}
					</div>
				)}
			</section>

			<section className="rounded-2xl border border-base-300 bg-base-200/35 p-5">
				<div className="flex gap-3">
					<Film className="mt-0.5 h-5 w-5 shrink-0 text-primary" />
					<div>
						<h3 className="font-bold">Portable and incremental</h3>
						<p className="mt-1 text-base-content/60 text-sm leading-relaxed">
							Each result is stored in a <code>.tater-markers.json</code> sidecar beside the video.
							Normal library monitoring queues only new or changed files; completed files are reused
							without another full analysis.
						</p>
					</div>
				</div>
			</section>

			<div className="flex justify-end border-base-300 border-t pt-5">
				<button
					type="button"
					className="btn btn-primary min-w-32"
					disabled={isReadOnly || !hasChanges || isUpdating}
					onClick={save}
				>
					{isUpdating ? (
						<span className="loading loading-spinner loading-sm" />
					) : (
						<Save className="h-4 w-4" />
					)}
					Save
				</button>
			</div>
		</div>
	);
}
