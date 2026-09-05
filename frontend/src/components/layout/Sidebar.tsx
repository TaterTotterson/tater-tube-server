import { useQuery } from "@tanstack/react-query";
import {
	Activity,
	ChevronLeft,
	ChevronRight,
	Cpu,
	Database,
	Home,
	ScrollText,
	Settings,
	Tv,
} from "lucide-react";
import { NavLink } from "react-router-dom";
import { apiClient } from "../../api/client";
import { useActiveStreams, useQueueStats } from "../../hooks/useApi";
import { useAuth } from "../../hooks/useAuth";
import { UserMenu } from "../auth/UserMenu";

const navigation = [
	{
		name: "Dashboard",
		href: "/",
		icon: Home,
	},
	{
		name: "TV Guide",
		href: "/tv-guide",
		icon: Tv,
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

interface SidebarProps {
	collapsed: boolean;
	onToggleCollapsed: () => void;
}

export function Sidebar({ collapsed, onToggleCollapsed }: SidebarProps) {
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
				return activeQueueCount;
			default:
				return 0;
		}
	};

	const getBadgeColor = (path: string, count: number) => {
		if (count === 0) return "";
		switch (path) {
			case "/queue":
				return queueStats && queueStats.total_processing > 0 ? "badge-warning" : "badge-info";
			default:
				return "badge-info";
		}
	};

	const statusLabel = () => {
		if (activeStreamCount > 0) return "streaming";
		if (activeQueueCount > 0) return "working";
		return "ready";
	};

	const queueLabel = () => {
		if (activeWorkCount > 0) return `${activeWorkCount} active`;
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
		<aside
			className={`min-h-full overflow-y-auto border-base-300 border-r bg-base-200/95 transition-[width] duration-200 ${collapsed ? "w-64 lg:w-20" : "w-64"}`}
		>
			<div className={`flex min-h-full flex-col p-4 ${collapsed ? "lg:px-3" : ""}`}>
				<div className={`mb-6 flex items-center gap-2 ${collapsed ? "lg:flex-col" : ""}`}>
					<NavLink
						to="/"
						className="min-w-0 flex-1 rounded-2xl border border-transparent px-1 py-2 transition hover:border-primary/15 hover:bg-primary/5"
						aria-label="Tater Tube dashboard"
					>
						<img
							src="/tater-tube-logo-leaning-transparent.png"
							alt="Tater Tube"
							className={`h-auto max-h-28 w-full object-contain ${collapsed ? "lg:hidden" : ""}`}
						/>
						<img
							src="/logo.png"
							alt=""
							className={`mx-auto hidden h-11 w-11 rounded-xl object-contain ${collapsed ? "lg:block" : ""}`}
						/>
					</NavLink>
					<button
						type="button"
						onClick={onToggleCollapsed}
						className="btn btn-square btn-ghost btn-sm hidden shrink-0 border border-base-300/70 lg:inline-flex"
						aria-label={collapsed ? "Expand navigation" : "Collapse navigation"}
						title={collapsed ? "Expand navigation" : "Collapse navigation"}
					>
						{collapsed ? <ChevronRight className="h-4 w-4" /> : <ChevronLeft className="h-4 w-4" />}
					</button>
				</div>

				<nav className="space-y-1.5" aria-label="Main navigation">
					{visibleNavigation.map((item) => {
						const badgeCount = getBadgeCount(item.href);
						const badgeColor = getBadgeColor(item.href, badgeCount);

						return (
							<NavLink
								key={item.name}
								to={item.href}
								className={({ isActive }) =>
									`flex items-center rounded-xl border py-2.5 font-medium transition-all ${collapsed ? "lg:justify-center lg:px-2" : "gap-3 px-3.5"} ${
										isActive
											? "border-primary/30 bg-primary/15 text-primary shadow-sm"
											: "border-transparent text-base-content/70 hover:border-base-300 hover:bg-base-100 hover:text-base-content"
									}`
								}
								title={collapsed ? item.name : undefined}
							>
								<item.icon className="h-[18px] w-[18px] shrink-0" aria-hidden="true" />
								<span className={`flex-1 ${collapsed ? "lg:sr-only" : ""}`}>{item.name}</span>
								{badgeCount > 0 && (
									<span className={`badge badge-sm ${badgeColor} ${collapsed ? "lg:hidden" : ""}`}>
										<span className="sr-only">{badgeCount} items</span>
										{badgeCount}
									</span>
								)}
							</NavLink>
						);
					})}
				</nav>

				<div className="mt-auto pt-8">
					<div className={collapsed ? "lg:hidden" : ""}>
						<div className="rounded-2xl border border-base-300 bg-base-100/65 p-3.5 shadow-sm">
							<div className="mb-3 text-[10px] text-base-content/40 uppercase tracking-[0.16em]">
								Server status
							</div>
							<div className="space-y-3">
								<div className="flex items-center justify-between">
									<div className="flex items-center space-x-2">
										<Activity className="h-4 w-4 text-success" />
										<span className="text-xs">Status</span>
									</div>
									<div className="font-medium text-base-content/75 text-xs capitalize">
										{statusLabel()}
									</div>
								</div>

								<div className="flex items-center justify-between">
									<div className="flex items-center space-x-2">
										<Database className="h-4 w-4" />
										<span className="text-xs">Activity</span>
									</div>
									<div className="font-medium text-base-content/75 text-xs">{queueLabel()}</div>
								</div>

								<div className="flex items-center justify-between">
									<div className="flex items-center space-x-2">
										<Cpu className="h-4 w-4 text-primary" />
										<span className="text-xs">Hardware</span>
									</div>
									<div className={`badge badge-sm ${hardwareBadgeClass()}`}>{hardwareLabel()}</div>
								</div>

								<div className="flex items-center justify-between">
									<div className="flex items-center space-x-2">
										<Tv className="h-4 w-4 text-primary" />
										<span className="text-xs">Streamer</span>
									</div>
									<div className="badge badge-success badge-sm">Online</div>
								</div>
							</div>
							<div className="mt-3 flex items-center justify-between border-base-300/70 border-t pt-3">
								<div className="text-base-content/45 text-xs">Version</div>
								<div className="font-mono text-base-content/70 text-xs">{__APP_VERSION__}</div>
							</div>
						</div>
						<div className="mt-3 border-base-300/70 border-t pt-3">
							<UserMenu />
						</div>
					</div>

					<div className={`hidden flex-col items-center gap-2 ${collapsed ? "lg:flex" : ""}`}>
						<div
							className="flex h-10 w-10 items-center justify-center rounded-xl border border-base-300 bg-base-100/65"
							title={`Server ${statusLabel()} · ${queueLabel()}`}
						>
							<Activity className="h-4 w-4 text-success" />
						</div>
						<UserMenu compact />
					</div>
				</div>
			</div>
		</aside>
	);
}
