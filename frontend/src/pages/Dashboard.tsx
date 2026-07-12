import { useQuery } from "@tanstack/react-query";
import {
	Activity,
	Cpu,
	Database,
	Gauge,
	HardDrive,
	List,
	Radio,
	Server,
	Tv,
	Users,
	Zap,
} from "lucide-react";
import { type ReactNode, useMemo } from "react";
import { apiClient } from "../api/client";
import {
	useActiveStreams,
	usePoolMetrics,
	useQueueStats,
	useStreamHistory,
	useSystemStats,
} from "../hooks/useApi";
import { useConfig } from "../hooks/useConfig";
import type { ActiveStream, HealthStats, SystemInfo } from "../types/api";

function formatBytes(value?: number) {
	if (!value || value <= 0) return "0 B";
	const units = ["B", "KB", "MB", "GB", "TB"];
	const exponent = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
	const amount = value / 1024 ** exponent;
	return `${amount.toFixed(amount >= 10 || exponent === 0 ? 0 : 1)} ${units[exponent]}`;
}

function formatRate(value?: number) {
	return `${formatBytes(value)}/s`;
}

function fileLabel(path: string) {
	const parts = path.split(/[\\/]/).filter(Boolean);
	return parts.at(-1) || path || "Unknown stream";
}

function timeAgo(value?: string) {
	if (!value) return "never";
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return "unknown";
	const seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000));
	if (seconds < 60) return "<1m ago";
	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${minutes}m ago`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h ago`;
	return `${Math.floor(hours / 24)}d ago`;
}

function streamActivityTime(stream?: ActiveStream) {
	return stream?.last_activity || stream?.started_at;
}

function playerNameKey(value?: string) {
	return (value || "").trim().replace(/\s+/g, " ").toLowerCase();
}

function isOnline(lastSeenAt?: string) {
	if (!lastSeenAt) return false;
	const age = Date.now() - new Date(lastSeenAt).getTime();
	return Number.isFinite(age) && age >= 0 && age < 5 * 60 * 1000;
}

function formatDuration(seconds?: number) {
	if (!Number.isFinite(seconds) || !seconds || seconds <= 0) return "00:00";
	const totalSeconds = Math.max(0, Math.floor(seconds));
	const hours = Math.floor(totalSeconds / 3600);
	const minutes = Math.floor((totalSeconds % 3600) / 60);
	const secs = totalSeconds % 60;
	if (hours > 0) {
		return `${hours}:${String(minutes).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
	}
	return `${minutes}:${String(secs).padStart(2, "0")}`;
}

function streamPlaybackPosition(stream: ActiveStream): number {
	if (Number.isFinite(stream.playback_position_seconds) && stream.playback_position_seconds > 0) {
		return stream.playback_position_seconds;
	}
	const started = new Date(stream.started_at);
	if (!Number.isNaN(started.getTime())) {
		return Math.max(0, (Date.now() - started.getTime()) / 1000);
	}
	return 0;
}

function streamPlaybackDuration(stream: ActiveStream): number {
	const duration = stream.media_duration_seconds ?? 0;
	if (Number.isFinite(duration) && duration > 0) {
		return duration;
	}
	return 0;
}

function streamProgress(stream: ActiveStream): number | undefined {
	const duration = streamPlaybackDuration(stream);
	if (duration <= 0) return undefined;
	return Math.max(0, Math.min(100, (streamPlaybackPosition(stream) / duration) * 100));
}

function hardwareName(value?: string) {
	switch (value) {
		case "vaapi":
			return "VAAPI";
		case "qsv":
			return "Quick Sync";
		case "nvenc":
			return "NVENC";
		case "videotoolbox":
			return "VideoToolbox";
		case "v4l2m2m":
			return "V4L2 M2M";
		default:
			return "Hardware";
	}
}

function playbackMode(stream?: ActiveStream) {
	if (!stream) {
		return null;
	}
	if (!stream.transcoded) {
		return {
			label: "Direct",
			className: "badge-outline",
			detail: "direct play",
		};
	}
	if (stream.hardware_active) {
		return {
			label: `HW ${hardwareName(stream.hardware_acceleration)}`,
			className: "badge-success",
			detail: stream.video_codec || stream.hardware_device || "hardware transcode",
		};
	}
	return {
		label: "SW Transcode",
		className: "badge-warning",
		detail: stream.transcode_name || stream.video_codec || "software transcode",
	};
}

function StatTile({
	icon: Icon,
	label,
	value,
	detail,
}: {
	icon: typeof Tv;
	label: string;
	value: string;
	detail: string;
}) {
	return (
		<div className="rounded-lg border border-base-300 bg-base-200/55 p-4">
			<div className="flex items-start gap-3">
				<div className="rounded-md bg-primary/15 p-2">
					<Icon className="h-5 w-5 text-primary" />
				</div>
				<div className="min-w-0">
					<div className="text-base-content/50 text-xs uppercase tracking-widest">{label}</div>
					<div className="mt-1 truncate font-vcr text-primary text-xl">{value}</div>
					<div className="mt-1 break-words text-base-content/60 text-sm leading-snug">{detail}</div>
				</div>
			</div>
		</div>
	);
}

function Panel({
	title,
	icon: Icon,
	children,
}: {
	title: string;
	icon: typeof Tv;
	children: ReactNode;
}) {
	return (
		<section className="min-w-0 overflow-hidden rounded-lg border border-base-300 bg-base-200/70 p-5">
			<div className="mb-4 flex min-w-0 items-center gap-2">
				<Icon className="h-5 w-5 text-primary" />
				<h2 className="tater-glow min-w-0 break-words font-vcr text-lg text-primary">{title}</h2>
			</div>
			{children}
		</section>
	);
}

export function Dashboard() {
	const { data: config } = useConfig();
	const { data: queueStats } = useQueueStats(10000);
	const { data: poolMetrics } = usePoolMetrics();
	const { data: activeStreamData } = useActiveStreams();
	const { data: streamHistoryData } = useStreamHistory();
	const { data: systemStats } = useSystemStats();
	const { data: transcodeDetection } = useQuery({
		queryKey: ["system", "transcoding-detect"],
		queryFn: () => apiClient.detectTranscodingHardware(),
		refetchInterval: 30000,
	});

	const activeStreams = Array.isArray(activeStreamData) ? activeStreamData : [];
	const streamHistory = Array.isArray(streamHistoryData) ? streamHistoryData : [];
	const providerMetrics = Array.isArray(poolMetrics?.providers) ? poolMetrics.providers : [];
	const configuredProviders = Array.isArray(config?.providers) ? config.providers : [];
	const systemInfo: Partial<SystemInfo> = systemStats?.system ?? {};
	const healthStats: Partial<HealthStats> = systemStats?.health ?? {};

	const players = useMemo(
		() =>
			(Array.isArray(config?.players?.players) ? config.players.players : []).filter(
				(player) => !player.revoked_at,
			),
		[config?.players?.players],
	);
	const onlinePlayers = players.filter((player) => isOnline(player.last_seen_at));
	const localMediaCategories = Array.isArray(config?.local_media?.categories)
		? config.local_media.categories
		: [];
	const localCategories = localMediaCategories.filter((category) => category.enabled !== false);
	const localPaths = localCategories.reduce(
		(total, category) => total + (category.paths?.length ?? 0),
		0,
	);
	const localTypeCounts = localCategories.reduce<Record<string, number>>((counts, category) => {
		const key = category.library_type || "folders";
		counts[key] = (counts[key] ?? 0) + 1;
		return counts;
	}, {});
	const activeQueueCount = queueStats
		? queueStats.total_processing +
			Math.max(
				0,
				queueStats.total_queued - queueStats.total_processing - queueStats.total_completed,
			)
		: 0;
	const providerCount = providerMetrics.length || configuredProviders.length;
	const hardwareOptions = transcodeDetection?.options?.filter((option) => option.available) ?? [];

	const streamByPlayerName = useMemo(() => {
		const map = new Map<string, ActiveStream>();
		for (const stream of activeStreams) {
			const key = playerNameKey(stream.user_name);
			if (key && !map.has(key)) {
				map.set(key, stream);
			}
		}
		return map;
	}, [activeStreams]);

	const streamByPlayerID = useMemo(() => {
		const map = new Map<string, ActiveStream>();
		for (const stream of activeStreams) {
			if (stream.player_id && !map.has(stream.player_id)) {
				map.set(stream.player_id, stream);
			}
		}
		return map;
	}, [activeStreams]);

	const lastStreamByPlayerName = useMemo(() => {
		const map = new Map<string, ActiveStream>();
		for (const stream of streamHistory) {
			const key = playerNameKey(stream.user_name);
			if (key && !map.has(key)) {
				map.set(key, stream);
			}
		}
		return map;
	}, [streamHistory]);

	const lastStreamByPlayerID = useMemo(() => {
		const map = new Map<string, ActiveStream>();
		for (const stream of streamHistory) {
			if (stream.player_id && !map.has(stream.player_id)) {
				map.set(stream.player_id, stream);
			}
		}
		return map;
	}, [streamHistory]);

	return (
		<div className="space-y-6">
			<section className="grid min-w-0 gap-4 xl:grid-cols-[minmax(320px,0.9fr)_minmax(0,1.1fr)]">
				<Panel title="Tater Tube Players" icon={Tv}>
					<div className="space-y-3">
						{players.length > 0 ? (
							players.map((player) => {
								const playerKey = playerNameKey(player.name);
								const stream = streamByPlayerID.get(player.id) || streamByPlayerName.get(playerKey);
								const lastStream =
									lastStreamByPlayerID.get(player.id) || lastStreamByPlayerName.get(playerKey);
								const mode = playbackMode(stream);
								const playerOnline = Boolean(stream) || isOnline(player.last_seen_at);
								return (
									<div
										key={player.id}
										className="min-w-0 overflow-hidden rounded-md border border-base-300 bg-base-100/70 p-3"
									>
										<div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-start sm:justify-between sm:gap-3">
											<div className="min-w-0 flex-1">
												<div className="dashboard-line-clamp font-semibold">{player.name}</div>
												<div className="text-base-content/60 text-sm">
													Last seen {timeAgo(player.last_seen_at)}
												</div>
											</div>
											<span
												className={`badge ${stream ? "badge-primary" : playerOnline ? "badge-success" : "badge-ghost"}`}
											>
												{stream ? "Playing" : playerOnline ? "Online" : "Idle"}
											</span>
										</div>
										<div className="mt-2 text-base-content/70 text-sm">
											{stream ? (
												<div className="flex min-w-0 flex-wrap items-center gap-2">
													<span className="dashboard-line-clamp min-w-0 flex-1">
														Playing {fileLabel(stream.file_path)}
													</span>
													{mode && (
														<span className={`badge badge-sm ${mode.className}`}>{mode.label}</span>
													)}
												</div>
											) : lastStream ? (
												<div className="min-w-0">
													<div className="dashboard-line-clamp">
														Last played {fileLabel(lastStream.file_path)}
													</div>
													<div className="mt-1 text-base-content/50 text-xs">
														Finished {timeAgo(streamActivityTime(lastStream))}
													</div>
												</div>
											) : (
												"No playback yet"
											)}
											{mode?.detail && (
												<div className="dashboard-line-clamp mt-1 text-base-content/50 text-xs">
													{mode.detail}
												</div>
											)}
										</div>
									</div>
								);
							})
						) : (
							<div className="rounded-md border border-base-300 border-dashed p-4 text-base-content/60 text-sm">
								No paired Tater Tube players yet.
							</div>
						)}
					</div>
				</Panel>

				<Panel title="Active Streams" icon={Gauge}>
					<div className="space-y-3">
						{activeStreams.length > 0 ? (
							activeStreams.map((stream) => {
								const mode = playbackMode(stream);
								const playbackPosition = streamPlaybackPosition(stream);
								const playbackDuration = streamPlaybackDuration(stream);
								const playbackProgress = streamProgress(stream);
								return (
									<div
										key={stream.id}
										className="min-w-0 overflow-hidden rounded-md border border-base-300 bg-base-100/70 p-3"
									>
										<div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-start sm:justify-between sm:gap-3">
											<div className="min-w-0 flex-1">
												<div className="dashboard-line-clamp font-semibold">
													{fileLabel(stream.file_path)}
												</div>
												<div className="dashboard-line-clamp text-base-content/60 text-sm">
													{stream.user_name || stream.source || "Unknown player"} -{" "}
													{formatRate(stream.bytes_per_second || stream.download_speed)}
												</div>
											</div>
											<div className="flex min-w-0 flex-wrap gap-2 sm:justify-end">
												{mode && <span className={`badge ${mode.className}`}>{mode.label}</span>}
												<span className="badge badge-primary">{stream.status || "Streaming"}</span>
											</div>
										</div>
										{mode?.detail && (
											<div className="dashboard-line-clamp mt-2 text-base-content/50 text-xs">
												{mode.detail}
											</div>
										)}
										<progress
											className="progress progress-primary mt-3 h-2 w-full"
											value={playbackProgress}
											max={100}
										/>
										<div className="mt-2 flex justify-between text-base-content/50 text-xs">
											<span>Playback {formatDuration(playbackPosition)}</span>
											<span>
												{playbackDuration > 0 ? `/ ${formatDuration(playbackDuration)}` : "elapsed"}
											</span>
										</div>
									</div>
								);
							})
						) : (
							<div className="rounded-md border border-base-300 border-dashed p-4 text-base-content/60 text-sm">
								No active streams.
							</div>
						)}
					</div>
				</Panel>
			</section>

			<section className="grid gap-4 xl:grid-cols-3">
				<Panel title="Server Specs" icon={Server}>
					<div className="grid gap-3 sm:grid-cols-2">
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">Host</div>
							<div className="truncate font-semibold">{systemInfo.hostname || "unknown"}</div>
						</div>
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">Runtime</div>
							<div className="font-semibold">
								{systemInfo.os || "?"}/{systemInfo.arch || "?"}
							</div>
						</div>
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">CPU</div>
							<div className="font-semibold">{systemInfo.cpus ?? 0} cores</div>
						</div>
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">Memory</div>
							<div className="font-semibold">{formatBytes(systemInfo.mem_sys)} reserved</div>
						</div>
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">Version</div>
							<div className="truncate font-semibold">{systemInfo.version || "dev"}</div>
						</div>
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">Uptime</div>
							<div className="truncate font-semibold">{systemInfo.uptime || "unknown"}</div>
						</div>
					</div>
				</Panel>

				<Panel title="Transcoding" icon={Cpu}>
					<div className="space-y-4">
						<div className="flex items-start justify-between gap-3">
							<div>
								<div className="text-base-content/50 text-xs uppercase tracking-widest">
									Detected GPU
								</div>
								<div className="font-semibold">
									{transcodeDetection?.recommended === "none"
										? "Software"
										: transcodeDetection?.recommended?.toUpperCase() || "checking"}
								</div>
								<div className="text-base-content/60 text-sm">
									{transcodeDetection?.recommended_device ||
										transcodeDetection?.ffmpeg_path ||
										"ffmpeg scan pending"}
								</div>
							</div>
							<div
								className={`badge ${
									transcodeDetection?.ffmpeg_available ? "badge-success" : "badge-warning"
								}`}
							>
								{transcodeDetection?.ffmpeg_available ? "Ready" : "No FFmpeg"}
							</div>
						</div>
						<div className="flex flex-wrap gap-2">
							{hardwareOptions.length > 0 ? (
								hardwareOptions.map((option) => (
									<span key={option.id} className="badge badge-outline">
										{option.label}
									</span>
								))
							) : (
								<span className="text-base-content/60 text-sm">
									No hardware encoder detected yet.
								</span>
							)}
						</div>
					</div>
				</Panel>

				<Panel title="Local Library" icon={HardDrive}>
					<div className="grid gap-3 sm:grid-cols-3 xl:grid-cols-1 2xl:grid-cols-3">
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">Status</div>
							<div className="font-semibold">
								{config?.local_media?.enabled ? "Enabled" : "Disabled"}
							</div>
						</div>
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">
								Categories
							</div>
							<div className="font-semibold">{localCategories.length}</div>
						</div>
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">Folders</div>
							<div className="font-semibold">{localPaths}</div>
						</div>
					</div>
					<div className="mt-4 flex flex-wrap gap-2">
						{Object.entries(localTypeCounts).length > 0 ? (
							Object.entries(localTypeCounts).map(([type, count]) => (
								<span key={type} className="badge badge-outline">
									{type}: {count}
								</span>
							))
						) : (
							<span className="text-base-content/60 text-sm">
								No local media categories configured.
							</span>
						)}
					</div>
				</Panel>
			</section>

			<section className="grid gap-4 md:grid-cols-2">
				<Panel title="Provider Load" icon={Database}>
					<div className="space-y-3">
						{providerMetrics.length ? (
							providerMetrics.map((provider) => (
								<div
									key={provider.id || provider.name}
									className="flex items-center justify-between gap-3"
								>
									<div className="min-w-0">
										<div className="truncate font-semibold">{provider.name || provider.host}</div>
										<div className="text-base-content/60 text-sm">
											{formatBytes(provider.byte_count_24h)} today
										</div>
									</div>
									<span
										className={`badge ${provider.state === "connected" ? "badge-success" : "badge-warning"}`}
									>
										{provider.state || "unknown"}
									</span>
								</div>
							))
						) : (
							<div className="text-base-content/60 text-sm">No provider metrics available.</div>
						)}
					</div>
				</Panel>

				<Panel title="Health" icon={Zap}>
					<div className="grid gap-3 sm:grid-cols-2">
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">Healthy</div>
							<div className="font-semibold">{healthStats.healthy ?? 0}</div>
						</div>
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">Checking</div>
							<div className="font-semibold">{healthStats.checking ?? 0}</div>
						</div>
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">Degraded</div>
							<div className="font-semibold">{healthStats.degraded ?? 0}</div>
						</div>
						<div>
							<div className="text-base-content/50 text-xs uppercase tracking-widest">
								Corrupted
							</div>
							<div className="font-semibold">{healthStats.corrupted ?? 0}</div>
						</div>
					</div>
				</Panel>
			</section>

			<section className="space-y-3">
				<div className="flex items-center gap-2">
					<Activity className="h-4 w-4 text-primary" />
					<h2 className="tater-glow font-vcr text-lg text-primary">System Summary</h2>
				</div>
				<div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
					<StatTile
						icon={Users}
						label="Players Online"
						value={`${onlinePlayers.length}/${players.length}`}
						detail="paired players seen in the last five minutes"
					/>
					<StatTile
						icon={Activity}
						label="Now Playing"
						value={String(activeStreams.length)}
						detail={`${formatRate(poolMetrics?.download_speed_bytes_per_sec)} current server pull`}
					/>
					<StatTile
						icon={Radio}
						label="NNTP Providers"
						value={String(providerCount)}
						detail={`${providerMetrics.filter((provider) => provider.state === "connected").length} connected`}
					/>
					<StatTile
						icon={List}
						label="Queue"
						value={`${activeQueueCount} active`}
						detail={`${queueStats?.total_failed ?? 0} failed items`}
					/>
				</div>
			</section>
		</div>
	);
}
