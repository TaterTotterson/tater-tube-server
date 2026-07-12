import { Activity, Cpu, Database, Home, ScrollText, Settings, Tv } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { NavLink } from "react-router-dom";
import { apiClient } from "../../api/client";
import { useActiveStreams, useQueueStats } from "../../hooks/useApi";
import { useAuth } from "../../hooks/useAuth";

const navigation = [
	{
		name: "Dashboard",
		href: "/",
		icon: Home,
	},
	{
		name: "Activity",
		href: "/queue",
		icon: Activity,
	},
	{
		name: "Logs",
		href: "/logs",
		icon: ScrollText,
	},
	{
		name: "Configuration",
		href: "/config",
		icon: Settings,
		adminOnly: true,
	},
];

export function Sidebar() {
	const { user, loginRequired } = useAuth();
	const { data: queueStats } = useQueueStats();
	const { data: activeStreamData } = useActiveStreams();
	const { data: transcodeDetection, isLoading: isDetectingHardware } = useQuery({
		queryKey: ["system", "transcoding-detect", "sidebar"],
		queryFn: () => apiClient.detectTranscodingHardware(),
		refetchInterval: 60000,
	});
	const activeStreams = Array.isArray(activeStreamData) ? activeStreamData : [];
	const activeStreamCount = activeStreams.length;
	const activeQueueCount = (() => {
		if (!queueStats) return 0;
		const totalItems =
			queueStats.total_processing + queueStats.total_completed + queueStats.total_failed;
		const pendingItems = Math.max(0, queueStats.total_queued - totalItems);
		return queueStats.total_processing + pendingItems;
	})();
	const activeWorkCount = activeQueueCount + activeStreamCount;

	const visibleNavigation = navigation.filter(
		(item) => !item.adminOnly || !loginRequired || (user?.is_admin ?? false),
	);

	const getBadgeCount = (path: string) => {
		switch (path) {
			case "/queue":
				return queueStats ? activeQueueCount + queueStats.total_failed : 0;
			default:
				return 0;
		}
	};

	const getBadgeColor = (path: string, count: number) => {
		if (count === 0) return "";
		switch (path) {
			case "/queue": {
				if (queueStats && queueStats.total_failed > 0) {
					return "badge-error";
				}
				return queueStats && queueStats.total_processing > 0 ? "badge-warning" : "badge-info";
			}
			default:
				return "badge-info";
		}
	};

	const statusLabel = () => {
		if (queueStats && queueStats.total_failed > 0) return "attention";
		if (activeStreamCount > 0) return "streaming";
		if (activeQueueCount > 0) return "working";
		return "ready";
	};

	const queueLabel = () => {
		if (activeWorkCount > 0) return `${activeWorkCount} active`;
		if (queueStats && queueStats.total_failed > 0) return `${queueStats.total_failed} failed`;
		return "idle";
	};

	const hardwareLabel = () => {
		if (isDetectingHardware) return "Checking";
		if (!transcodeDetection?.ffmpeg_available) return "No FFmpeg";
		switch (transcodeDetection.recommended) {
			case "vaapi":
				return "VAAPI";
			case "qsv":
				return "QSV";
			case "nvenc":
				return "NVENC";
			case "videotoolbox":
				return "VTB";
			case "v4l2m2m":
				return "V4L2";
			default:
				return "Software";
		}
	};

	const hardwareBadgeClass = () => {
		if (isDetectingHardware) return "badge-ghost";
		if (!transcodeDetection?.ffmpeg_available) return "badge-warning";
		return transcodeDetection.recommended && transcodeDetection.recommended !== "none"
			? "badge-success"
			: "badge-ghost";
	};

	return (
		<aside className="min-h-full w-44 overflow-y-auto bg-base-200 md:w-48 lg:w-52">
			{" "}
			<div className="p-4">
				<div className="mb-8 flex items-center space-x-3">
					<div className="avatar placeholder">
						<div className="flex h-12 w-12 items-center justify-center overflow-hidden">
							<img src="/logo.png" alt="Tater Tube Server" className="h-12 w-12 object-contain" />
						</div>
					</div>
					<div>
						<h2 className="tater-glow font-vcr text-lg text-primary leading-tight">Tater Tube</h2>
					</div>
				</div>

				<nav className="space-y-2" aria-label="Main navigation">
					{visibleNavigation.map((item) => {
						const badgeCount = getBadgeCount(item.href);
						const badgeColor = getBadgeColor(item.href, badgeCount);

						return (
							<NavLink
								key={item.name}
								to={item.href}
								className={({ isActive }) =>
									`flex items-center space-x-3 rounded-lg px-4 py-3 transition-colors ${
										isActive ? "bg-primary text-primary-content" : "hover:bg-base-300"
									}`
								}
							>
								<item.icon className="h-5 w-5" aria-hidden="true" />
								<span className="flex-1">{item.name}</span>
								{badgeCount > 0 && (
									<span className={`badge badge-sm ${badgeColor}`}>
										<span className="sr-only">{badgeCount} items</span>
										{badgeCount}
									</span>
								)}
							</NavLink>
						);
					})}
				</nav>

				<div className="mt-8 border-base-300 border-t pt-6">
					<div className="space-y-4">
						<div className="flex items-center justify-between">
							<div className="flex items-center space-x-2">
								<Activity className="h-4 w-4 text-success" />
								<span className="text-sm">Status</span>
							</div>
							<div className="text-base-content/70 text-sm">{statusLabel()}</div>
						</div>

						<div className="flex items-center justify-between">
							<div className="flex items-center space-x-2">
								<Database className="h-4 w-4" />
								<span className="text-sm">Activity</span>
							</div>
							<div className="text-base-content/70 text-sm">{queueLabel()}</div>
						</div>

						<div className="flex items-center justify-between">
							<div className="flex items-center space-x-2">
								<Cpu className="h-4 w-4 text-primary" />
								<span className="text-sm">HW</span>
							</div>
							<div className={`badge badge-sm ${hardwareBadgeClass()}`}>{hardwareLabel()}</div>
						</div>

						<div className="flex items-center justify-between">
							<div className="flex items-center space-x-2">
								<Tv className="h-4 w-4 text-primary" />
								<span className="text-sm">Streamer</span>
							</div>
							<div className="badge badge-success badge-sm">Online</div>
						</div>
					</div>
				</div>

				<div className="mt-4 border-base-300 border-t pt-4">
					<div className="space-y-2">
						<div className="flex items-center justify-between">
							<div className="text-base-content/70 text-sm">Version</div>
							<div className="font-mono text-base-content text-sm">{__APP_VERSION__}</div>
						</div>
					</div>
				</div>
			</div>
		</aside>
	);
}
