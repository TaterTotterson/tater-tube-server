import {
	Activity,
	Clock3,
	Cpu,
	Film,
	HardDrive,
	MonitorPlay,
	Radio,
	RefreshCw,
	Search,
	Server,
	Tv,
	Zap,
} from "lucide-react";
import { useMemo, useState } from "react";
import { ErrorAlert } from "../components/ui/ErrorAlert";
import { LoadingSpinner } from "../components/ui/LoadingSpinner";
import { Pagination } from "../components/ui/Pagination";
import { useStreamHistory } from "../hooks/useApi";
import type { ActiveStream } from "../types/api";

type SourceFilter = "all" | "tube-tv" | "local" | "nzb" | "other";

function mediaTitle(path?: string) {
	const value = String(path || "").trim();
	if (value.toLowerCase().startsWith("tube tv ch ")) return value;
	const parts = value.split(/[\\/]/).filter(Boolean);
	return parts.at(-1) || value || "Unknown media";
}

function playerLabel(stream: ActiveStream) {
	return stream.user_name || stream.player_id || stream.client_ip || "Unknown player";
}

function sourceBucket(stream: ActiveStream): SourceFilter {
	const source = String(stream.source || "").toLowerCase();
	if (source === "tube tv") return "tube-tv";
	if (source === "local") return "local";
	if (source === "api" || source === "stremio" || source.includes("nzb")) return "nzb";
	return "other";
}

function sourceLabel(stream: ActiveStream) {
	switch (sourceBucket(stream)) {
		case "tube-tv":
			return "Tube TV";
		case "local":
			return "Local";
		case "nzb":
			return "NZB Stream";
		default:
			return stream.source || "Playback";
	}
}

function sourceClass(stream: ActiveStream) {
	switch (sourceBucket(stream)) {
		case "tube-tv":
			return "border-primary/50 bg-primary/10 text-primary";
		case "local":
			return "border-success/40 bg-success/10 text-success";
		case "nzb":
			return "border-info/40 bg-info/10 text-info";
		default:
			return "border-base-300 bg-base-200 text-base-content/70";
	}
}

function activityDate(stream: ActiveStream) {
	const value = stream.last_activity || stream.started_at;
	const date = new Date(value);
	return Number.isNaN(date.getTime()) ? undefined : date;
}

function watchedSeconds(stream: ActiveStream) {
	if (Number.isFinite(stream.watched_seconds) && stream.watched_seconds > 0) {
		return stream.watched_seconds;
	}
	const started = new Date(stream.started_at).getTime();
	const ended = activityDate(stream)?.getTime() ?? started;
	return Number.isFinite(started) && Number.isFinite(ended)
		? Math.max(0, (ended - started) / 1000)
		: 0;
}

function formatDuration(value?: number) {
	if (!Number.isFinite(value) || !value || value <= 0) return "0:00";
	const total = Math.max(0, Math.floor(value));
	const hours = Math.floor(total / 3600);
	const minutes = Math.floor((total % 3600) / 60);
	const seconds = total % 60;
	if (hours > 0) {
		return `${hours}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
	}
	return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

function formatBytes(value?: number) {
	if (!value || value <= 0) return "0 B";
	const units = ["B", "KB", "MB", "GB", "TB"];
	const exponent = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
	const amount = value / 1024 ** exponent;
	return `${amount.toFixed(amount >= 10 || exponent === 0 ? 0 : 1)} ${units[exponent]}`;
}

function timeAgo(value?: Date) {
	if (!value) return "Unknown";
	const seconds = Math.max(0, Math.floor((Date.now() - value.getTime()) / 1000));
	if (seconds < 10) return "Just now";
	if (seconds < 60) return `${seconds}s ago`;
	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${minutes}m ago`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h ago`;
	return `${Math.floor(hours / 24)}d ago`;
}

function dayLabel(date?: Date) {
	if (!date) return "Unknown date";
	const today = new Date();
	const yesterday = new Date();
	yesterday.setDate(today.getDate() - 1);
	if (date.toDateString() === today.toDateString()) return "Today";
	if (date.toDateString() === yesterday.toDateString()) return "Yesterday";
	return new Intl.DateTimeFormat(undefined, {
		weekday: "long",
		month: "short",
		day: "numeric",
		year: date.getFullYear() === today.getFullYear() ? undefined : "numeric",
	}).format(date);
}

function clockLabel(date?: Date) {
	if (!date) return "--:--";
	return new Intl.DateTimeFormat(undefined, {
		hour: "numeric",
		minute: "2-digit",
		second: "2-digit",
	}).format(date);
}

function isLive(stream: ActiveStream) {
	const status = String(stream.status || "").toLowerCase();
	const date = activityDate(stream);
	const recent = date ? Date.now() - date.getTime() < 20000 : false;
	return recent && ["starting", "buffering", "streaming", "transcoding"].includes(status);
}

function playbackProgress(stream: ActiveStream) {
	const duration = Number(stream.media_duration_seconds || 0);
	const position = Number(stream.playback_position_seconds || 0);
	if (!Number.isFinite(duration) || duration <= 0 || !Number.isFinite(position)) return undefined;
	return Math.max(0, Math.min(100, (position / duration) * 100));
}

function hardwareLabel(stream: ActiveStream) {
	if (!stream.transcoded) return "Direct Play";
	if (!stream.hardware_active) return "Software Transcode";
	switch (String(stream.hardware_acceleration || "").toLowerCase()) {
		case "qsv":
			return "HW Quick Sync";
		case "vaapi":
			return "HW VAAPI";
		case "nvenc":
			return "HW NVENC";
		case "videotoolbox":
			return "HW VideoToolbox";
		case "v4l2m2m":
			return "HW V4L2";
		default:
			return "Hardware Transcode";
	}
}

function hardwareDetail(stream: ActiveStream) {
	const parts = [stream.video_codec, stream.transcode_name || stream.transcode_profile]
		.filter(Boolean)
		.map((value) => String(value));
	return parts.join(" / ");
}

export function QueuePage() {
	const [search, setSearch] = useState("");
	const [source, setSource] = useState<SourceFilter>("all");
	const [page, setPage] = useState(1);
	const [pageSize, setPageSize] = useState(25);
	const { data, isLoading, isFetching, error, refetch } = useStreamHistory();
	const history = useMemo(() => (Array.isArray(data) ? data : []), [data]);

	const filtered = useMemo(() => {
		const needle = search.trim().toLowerCase();
		return history.filter((stream) => {
			if (source !== "all" && sourceBucket(stream) !== source) return false;
			if (!needle) return true;
			return [
				mediaTitle(stream.file_path),
				stream.file_path,
				playerLabel(stream),
				sourceLabel(stream),
				stream.video_codec,
				stream.hardware_acceleration,
			]
				.filter(Boolean)
				.some((value) => String(value).toLowerCase().includes(needle));
		});
	}, [history, search, source]);
	const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize));
	const currentPage = Math.min(page, totalPages);
	const paged = useMemo(() => {
		const start = (currentPage - 1) * pageSize;
		return filtered.slice(start, start + pageSize);
	}, [currentPage, filtered, pageSize]);

	const grouped = useMemo(() => {
		const rows: Array<{ label: string; records: ActiveStream[] }> = [];
		for (const stream of paged) {
			const label = dayLabel(activityDate(stream));
			const current = rows.at(-1);
			if (!current || current.label !== label) {
				rows.push({ label, records: [stream] });
			} else {
				current.records.push(stream);
			}
		}
		return rows;
	}, [paged]);

	const uniquePlayers = useMemo(
		() => new Set(history.map((stream) => playerLabel(stream).toLowerCase())).size,
		[history],
	);
	const watchedTotal = useMemo(
		() => history.reduce((total, stream) => total + watchedSeconds(stream), 0),
		[history],
	);
	const hardwarePlays = useMemo(
		() => history.filter((stream) => stream.transcoded && stream.hardware_active).length,
		[history],
	);
	const liveCount = useMemo(() => history.filter(isLive).length, [history]);

	if (error) {
		return (
			<div className="space-y-5">
				<h1 className="font-bold text-3xl">Activity</h1>
				<ErrorAlert error={error as Error} onRetry={() => refetch()} />
			</div>
		);
	}

	return (
		<div className="space-y-6 pb-8">
			<header className="flex min-w-0 items-center justify-between gap-4 border-base-300 border-b pb-5">
				<div className="flex min-w-0 items-center gap-4">
					<div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-lg border border-primary/40 bg-primary/10">
						<Activity className="h-6 w-6 text-primary" />
					</div>
					<div className="min-w-0">
						<div className="font-mono text-primary text-xs uppercase tracking-widest">Tape Log</div>
						<h1 className="truncate font-bold text-2xl sm:text-3xl">Playback Activity</h1>
					</div>
				</div>
				<div className="flex shrink-0 items-center gap-3">
					<img
						src="/tater-tube-server-mascot.png"
						alt=""
						className="hidden h-16 w-16 object-contain sm:block"
					/>
					<button
						type="button"
						className="btn btn-square btn-outline btn-sm"
						onClick={() => refetch()}
						disabled={isFetching}
						title="Refresh playback activity"
						aria-label="Refresh playback activity"
					>
						{isFetching ? <LoadingSpinner size="sm" /> : <RefreshCw className="h-4 w-4" />}
					</button>
				</div>
			</header>

			<section className="grid overflow-hidden rounded-lg border border-base-300 bg-base-100 sm:grid-cols-2 xl:grid-cols-4">
				<div className="flex items-center gap-3 border-base-300 border-b p-4 sm:border-r xl:border-b-0">
					<Radio className="h-5 w-5 shrink-0 text-error" />
					<div>
						<div className="font-mono text-[11px] text-base-content/45 uppercase">On Air</div>
						<div className="font-bold text-xl">{liveCount}</div>
					</div>
				</div>
				<div className="flex items-center gap-3 border-base-300 border-b p-4 xl:border-r xl:border-b-0">
					<MonitorPlay className="h-5 w-5 shrink-0 text-primary" />
					<div>
						<div className="font-mono text-[11px] text-base-content/45 uppercase">Players</div>
						<div className="font-bold text-xl">{uniquePlayers}</div>
					</div>
				</div>
				<div className="flex items-center gap-3 border-base-300 border-b p-4 sm:border-r sm:border-b-0">
					<Clock3 className="h-5 w-5 shrink-0 text-secondary" />
					<div>
						<div className="font-mono text-[11px] text-base-content/45 uppercase">Watched</div>
						<div className="font-bold text-xl">{formatDuration(watchedTotal)}</div>
					</div>
				</div>
				<div className="flex items-center gap-3 p-4">
					<Cpu className="h-5 w-5 shrink-0 text-success" />
					<div>
						<div className="font-mono text-[11px] text-base-content/45 uppercase">HW Plays</div>
						<div className="font-bold text-xl">{hardwarePlays}</div>
					</div>
				</div>
			</section>

			<div className="flex flex-col gap-3 sm:flex-row">
				<label className="input input-bordered flex min-w-0 flex-1 items-center gap-2">
					<Search className="h-4 w-4 shrink-0 text-base-content/40" />
					<input
						type="search"
						className="min-w-0 grow"
						placeholder="Search playback"
						value={search}
						onChange={(event) => {
							setSearch(event.target.value);
							setPage(1);
						}}
					/>
				</label>
				<select
					className="select select-bordered w-full sm:w-44"
					value={source}
					onChange={(event) => {
						setSource(event.target.value as SourceFilter);
						setPage(1);
					}}
					aria-label="Filter playback source"
				>
					<option value="all">All Sources</option>
					<option value="tube-tv">Tube TV</option>
					<option value="local">Local</option>
					<option value="nzb">NZB Stream</option>
					<option value="other">Other</option>
				</select>
				<select
					className="select select-bordered w-full sm:w-36"
					value={pageSize}
					onChange={(event) => {
						setPageSize(Number(event.target.value));
						setPage(1);
					}}
					aria-label="Playback entries per page"
				>
					<option value={10}>10 per page</option>
					<option value={25}>25 per page</option>
					<option value={50}>50 per page</option>
				</select>
			</div>

			{!isLoading && filtered.length > 0 && (
				<Pagination
					currentPage={currentPage}
					totalPages={totalPages}
					onPageChange={setPage}
					totalItems={filtered.length}
					itemsPerPage={pageSize}
				/>
			)}

			{isLoading ? (
				<div className="flex min-h-64 items-center justify-center">
					<LoadingSpinner />
				</div>
			) : grouped.length === 0 ? (
				<div className="flex min-h-72 flex-col items-center justify-center rounded-lg border border-base-300 border-dashed bg-base-100/50 px-6 text-center">
					<img
						src="/tater-tube-server-mascot.png"
						alt=""
						className="mb-4 h-28 w-28 object-contain opacity-70"
					/>
					<div className="font-bold text-lg">No playback activity</div>
					<div className="mt-1 text-base-content/45 text-sm">STANDBY / REC 00:00:00</div>
				</div>
			) : (
				<div className="space-y-7">
					{grouped.map((group) => (
						<section key={group.label} className="space-y-3">
							<div className="flex items-center gap-3">
								<div className="font-bold font-mono text-primary text-xs uppercase tracking-widest">
									{group.label}
								</div>
								<div className="h-px flex-1 bg-base-300" />
								<div className="font-mono text-base-content/35 text-xs">{group.records.length}</div>
							</div>

							<div className="space-y-3">
								{group.records.map((stream) => {
									const moment = activityDate(stream);
									const live = isLive(stream);
									const progress = playbackProgress(stream);
									const hardware = hardwareLabel(stream);
									const detail = hardwareDetail(stream);
									return (
										<article
											key={`${stream.id}-${stream.started_at}`}
											className="overflow-hidden rounded-lg border border-base-300 bg-base-100 shadow-sm"
										>
											<div className="grid min-w-0 gap-4 p-4 md:grid-cols-[minmax(0,1.8fr)_minmax(12rem,0.8fr)_minmax(13rem,0.9fr)] md:items-center">
												<div className="flex min-w-0 items-start gap-3">
													<div className="mt-0.5 flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-base-200">
														{sourceBucket(stream) === "tube-tv" ? (
															<Tv className="h-5 w-5 text-primary" />
														) : sourceBucket(stream) === "nzb" ? (
															<Server className="h-5 w-5 text-info" />
														) : sourceBucket(stream) === "local" ? (
															<HardDrive className="h-5 w-5 text-success" />
														) : (
															<Film className="h-5 w-5 text-base-content/60" />
														)}
													</div>
													<div className="min-w-0 flex-1">
														<div className="flex min-w-0 flex-wrap items-center gap-2">
															<h2
																className="min-w-0 truncate font-bold text-base"
																title={stream.file_path}
															>
																{mediaTitle(stream.file_path)}
															</h2>
															{live && (
																<span className="inline-flex items-center gap-1 rounded border border-error/40 bg-error/10 px-1.5 py-0.5 font-mono text-[10px] text-error uppercase">
																	<span className="h-1.5 w-1.5 rounded-full bg-error" />
																	On Air
																</span>
															)}
														</div>
														<div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs">
															<span
																className={`rounded border px-1.5 py-0.5 font-semibold ${sourceClass(stream)}`}
															>
																{sourceLabel(stream)}
															</span>
															<span className="truncate text-base-content/50">
																{stream.file_path}
															</span>
														</div>
														{progress !== undefined && (
															<div className="mt-3 flex items-center gap-3">
																<div className="h-1.5 min-w-20 flex-1 overflow-hidden rounded bg-base-300">
																	<div
																		className="h-full bg-primary"
																		style={{ width: `${progress}%` }}
																	/>
																</div>
																<span className="shrink-0 font-mono text-[11px] text-base-content/45">
																	{formatDuration(stream.playback_position_seconds)} /{" "}
																	{formatDuration(stream.media_duration_seconds)}
																</span>
															</div>
														)}
													</div>
												</div>

												<div className="min-w-0">
													<div className="font-mono text-[10px] text-base-content/40 uppercase tracking-widest">
														Player
													</div>
													<div className="mt-1 truncate font-semibold text-sm">
														{playerLabel(stream)}
													</div>
													<div className="mt-1 flex flex-wrap gap-x-3 text-base-content/45 text-xs">
														<span>Watched {formatDuration(watchedSeconds(stream))}</span>
														{stream.bytes_sent > 0 && (
															<span>{formatBytes(stream.bytes_sent)} sent</span>
														)}
													</div>
												</div>

												<div className="min-w-0 md:text-right">
													<div className="flex items-center gap-2 md:justify-end">
														{stream.hardware_active ? (
															<Zap className="h-4 w-4 shrink-0 text-success" />
														) : (
															<Cpu className="h-4 w-4 shrink-0 text-base-content/50" />
														)}
														<span className="truncate font-semibold text-sm">{hardware}</span>
													</div>
													{detail && (
														<div className="mt-1 truncate font-mono text-[11px] text-base-content/45">
															{detail}
														</div>
													)}
													<div className="mt-2 font-mono text-base-content/55 text-xs">
														{clockLabel(moment)} / {timeAgo(moment)}
													</div>
												</div>
											</div>
										</article>
									);
								})}
							</div>
						</section>
					))}
					<Pagination
						currentPage={currentPage}
						totalPages={totalPages}
						onPageChange={setPage}
						totalItems={filtered.length}
						itemsPerPage={pageSize}
					/>
				</div>
			)}
		</div>
	);
}
