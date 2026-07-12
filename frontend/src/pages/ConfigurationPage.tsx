import {
	Activity,
	AlertTriangle,
	Cog,
	Cpu,
	Download,
	Folder,
	HardDrive,
	Link,
	Radio,
	RefreshCw,
	Server,
	Settings,
	Shield,
	ShieldAlert,
	Tv,
	Wrench,
} from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ArrsConfigSection } from "../components/config/ArrsConfigSection";
import { AuthConfigSection } from "../components/config/AuthConfigSection";
import { ComingSoonSection } from "../components/config/ComingSoonSection";
import { HealthConfigSection } from "../components/config/HealthConfigSection";
import { LocalMediaConfigSection } from "../components/config/LocalMediaConfigSection";
import { MetadataConfigSection } from "../components/config/MetadataConfigSection";
import { NewznabConfigSection } from "../components/config/NewznabConfigSection";
import { ProvidersConfigSection } from "../components/config/ProvidersConfigSection";
import { SABnzbdConfigSection } from "../components/config/SABnzbdConfigSection";
import { StreamingConfigSection } from "../components/config/StreamingConfigSection";
import { SystemConfigSection } from "../components/config/SystemConfigSection";
import { TaterPlayersConfigSection } from "../components/config/TaterPlayersConfigSection";
import { TranscodingConfigSection } from "../components/config/TranscodingConfigSection";
import { ImportConfigSection } from "../components/config/WorkersConfigSection";
import { ErrorAlert } from "../components/ui/ErrorAlert";
import { LoadingSpinner } from "../components/ui/LoadingSpinner";
import { RestartRequiredBanner } from "../components/ui/RestartRequiredBanner";
import { useConfirm } from "../contexts/ModalContext";
import { useAuth } from "../hooks/useAuth";
import {
	useConfig,
	useReloadConfig,
	useRestartServer,
	useUpdateConfigSection,
} from "../hooks/useConfig";
import type {
	ArrsConfig,
	AuthConfig,
	ConfigSection,
	HealthConfig,
	ImportConfig,
	LocalMediaConfig,
	LogFormData,
	MetadataConfig,
	NewznabConfig,
	ProviderConfig,
	SABnzbdConfig,
	SegmentCacheConfig,
	StreamingConfig,
	TranscodingConfig,
} from "../types/config";
import { CONFIG_SECTIONS } from "../types/config";

// Helper function to get icon component
const getIconComponent = (iconName: string) => {
	const iconMap = {
		Folder,
		Download,
		Shield,
		Cog,
		Cpu,
		Radio,
		HardDrive,
		ShieldAlert,
		Activity,
		Wrench,
		Server,
		Tv,
		Link,
	};
	return iconMap[iconName as keyof typeof iconMap] || Settings;
};

// Define section groups for modern organization
const SECTION_GROUPS = [
	{
		title: "Streamer",
		sections: ["players", "local_media", "newznab", "providers"],
	},
	{
		title: "Processing",
		sections: ["transcoding", "import", "streaming", "metadata"],
	},
	{
		title: "Access",
		sections: ["auth"],
	},
	{
		title: "System",
		sections: ["system"],
	},
];

const STREAMER_CONFIG_SECTIONS = new Set<ConfigSection | "system">(
	SECTION_GROUPS.flatMap((group) => group.sections) as (ConfigSection | "system")[],
);

export function ConfigurationPage() {
	const { data: config, isLoading, error, refetch } = useConfig();
	const reloadConfig = useReloadConfig();
	const restartServer = useRestartServer();
	const updateConfigSection = useUpdateConfigSection();
	const { recheckAuth } = useAuth();
	const { confirmAction } = useConfirm();
	const navigate = useNavigate();
	const { section } = useParams<{ section: string }>();

	// Get active section from URL parameter, default to player setup.
	const activeSection = (() => {
		if (!section) return "players";
		return STREAMER_CONFIG_SECTIONS.has(section as ConfigSection | "system")
			? (section as ConfigSection | "system")
			: "players";
	})();

	// Redirect to default section if no section is specified, or hidden legacy paths are requested.
	useEffect(() => {
		if (!section) {
			navigate("/config/players", { replace: true });
		} else if (!STREAMER_CONFIG_SECTIONS.has(section as ConfigSection | "system")) {
			navigate("/config/players", { replace: true });
		}
	}, [section, navigate]);

	const [restartRequiredConfigs, setRestartRequiredConfigs] = useState<string[]>([]);
	const [isRestartBannerDismissed, setIsRestartBannerDismissed] = useState(() => {
		// Initialize from session storage on component mount
		return sessionStorage.getItem("restartBannerDismissed") === "true";
	});

	// Helper functions for restart required state
	const addRestartRequiredConfig = (configName: string) => {
		setRestartRequiredConfigs((prev) => (prev.includes(configName) ? prev : [...prev, configName]));
		setIsRestartBannerDismissed(false);
	};

	const handleDismissRestartBanner = () => {
		setIsRestartBannerDismissed(true);
		sessionStorage.setItem("restartBannerDismissed", "true");
	};

	// Clear restart state on config reload (indicates server restart)
	const handleReloadConfig = async () => {
		try {
			await reloadConfig.mutateAsync();
			setRestartRequiredConfigs([]);
			setIsRestartBannerDismissed(false);
			sessionStorage.removeItem("restartBannerDismissed");
		} catch (error) {
			console.error("Failed to reload configuration:", error);
		}
	};

	// Handle server restart
	const handleRestartServer = async () => {
		const confirmed = await confirmAction(
			"Restart Server",
			"This will restart the entire server. All active connections will be lost. Continue?",
			{
				type: "error",
				confirmText: "Restart Server",
				confirmButtonClass: "btn-error",
			},
		);
		if (!confirmed) {
			return;
		}

		try {
			await restartServer.mutateAsync(false);
			// Clear local state since server is restarting
			setRestartRequiredConfigs([]);
			setIsRestartBannerDismissed(false);
			sessionStorage.removeItem("restartBannerDismissed");

			// Wait a bit for the server to restart, then reload the page
			setTimeout(() => {
				window.location.reload();
			}, 3000);
		} catch (error) {
			console.error("Failed to restart server:", error);
		}
	};

	// Handle configuration updates
	// biome-ignore lint/suspicious/noExplicitAny: accepts various config types
	const handleConfigUpdate = async (section: string, data: any) => {
		try {
			if (section === "auth") {
				await updateConfigSection.mutateAsync({
					section: "auth",
					config: { auth: data as unknown as AuthConfig },
				});
				// Re-evaluate auth state so loginRequired reflects the new config immediately
				await recheckAuth();
			} else if (section === "streaming") {
				await updateConfigSection.mutateAsync({
					section: "streaming",
					config: { streaming: data as unknown as StreamingConfig },
				});
			} else if (section === "transcoding") {
				await updateConfigSection.mutateAsync({
					section: "transcoding",
					config: { transcoding: data as unknown as TranscodingConfig },
				});
			} else if (section === "segment_cache") {
				await updateConfigSection.mutateAsync({
					section: "segment_cache",
					config: { segment_cache: data as unknown as SegmentCacheConfig },
				});
			} else if (section === "import") {
				await updateConfigSection.mutateAsync({
					section: "import",
					config: { import: data as unknown as ImportConfig },
				});
			} else if (section === "metadata" && config) {
				const metadataData = data as unknown as MetadataConfig;
				const rootPathChanged = metadataData.root_path !== config.metadata.root_path;
				await updateConfigSection.mutateAsync({
					section: "metadata",
					config: { metadata: metadataData },
				});
				if (rootPathChanged) addRestartRequiredConfig("Metadata Root Path");
			} else if (section === "sabnzbd") {
				await updateConfigSection.mutateAsync({
					section: "sabnzbd",
					config: { sabnzbd: data as unknown as SABnzbdConfig },
				});
			} else if (section === "arrs") {
				await updateConfigSection.mutateAsync({
					section: "arrs",
					config: { arrs: data as unknown as ArrsConfig },
				});
			} else if (section === "health") {
				await updateConfigSection.mutateAsync({
					section: "health",
					config: { health: data as unknown as HealthConfig },
				});
			} else if (section === "newznab") {
				await updateConfigSection.mutateAsync({
					section: "newznab",
					config: { newznab: data as unknown as NewznabConfig },
				});
			} else if (section === "local_media") {
				await updateConfigSection.mutateAsync({
					section: "local_media",
					config: { local_media: data as unknown as LocalMediaConfig },
				});
			} else if (section === "providers") {
				await updateConfigSection.mutateAsync({
					section: "providers",
					config: { providers: data as unknown as ProviderConfig[] },
				});
			} else if (section === "log") {
				const logData = data as unknown as LogFormData & { profiler_enabled?: boolean };
				const { profiler_enabled, ...logConfig } = logData;
				await updateConfigSection.mutateAsync({
					section: "system",
					config: {
						log: logConfig,
						profiler_enabled: profiler_enabled,
					},
				});
			}
		} catch (error) {
			console.error("Failed to update configuration:", error);
			throw error;
		}
	};

	if (isLoading) {
		return (
			<div className="flex h-[60vh] w-full items-center justify-center">
				<LoadingSpinner size="lg" />
			</div>
		);
	}

	if (error) {
		return (
			<div className="space-y-4">
				<h1 className="font-bold text-3xl">Configuration</h1>
				<ErrorAlert error={error as Error} onRetry={() => refetch()} />
			</div>
		);
	}

	if (!config) {
		return (
			<div className="space-y-4">
				<h1 className="font-bold text-3xl">Configuration</h1>
				<div className="alert alert-warning">
					<AlertTriangle className="h-6 w-6" />
					<div>
						<div className="font-bold">Configuration Not Available</div>
						<div className="text-sm">Unable to load configuration.</div>
					</div>
				</div>
			</div>
		);
	}

	return (
		<div className="space-y-6">
			{/* Header */}
			<div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-center">
				<div className="flex items-center space-x-3">
					<div className="shrink-0 rounded-xl bg-primary/10 p-2 shadow-inner">
						<Settings className="h-8 w-8 text-primary" />
					</div>
					<div className="min-w-0">
						<h1 className="truncate font-bold text-2xl tracking-tight sm:text-3xl">
							Configuration
						</h1>
						<p className="text-base-content/60 text-xs sm:text-sm">
							Streamer setup and service settings.
						</p>
					</div>
				</div>

				<div className="flex flex-wrap items-center gap-2 sm:flex-nowrap">
					<button
						type="button"
						className="btn btn-ghost btn-sm border-base-300 bg-base-100 hover:bg-base-200"
						onClick={handleReloadConfig}
						disabled={reloadConfig.isPending}
					>
						{reloadConfig.isPending ? (
							<LoadingSpinner size="sm" />
						) : (
							<RefreshCw className="h-4 w-4" />
						)}
						Reload
					</button>

					<button
						type="button"
						className="btn btn-error btn-outline btn-sm"
						onClick={handleRestartServer}
						disabled={restartServer.isPending}
					>
						{restartServer.isPending ? <LoadingSpinner size="sm" /> : <Radio className="h-4 w-4" />}
						Restart
					</button>
				</div>
			</div>

			<RestartRequiredBanner
				restartRequiredConfigs={restartRequiredConfigs}
				onDismiss={handleDismissRestartBanner}
				isDismissed={isRestartBannerDismissed}
			/>

			<div className="grid grid-cols-1 gap-6 lg:grid-cols-12">
				{/* Modern Sidebar (Stacks on mobile, exactly like Import) */}
				<div className="lg:col-span-3 xl:col-span-2">
					{" "}
					<div className="card border border-base-200 bg-base-100/50 shadow-sm backdrop-blur-md sm:sticky sm:top-24">
						<div className="card-body p-2 sm:p-4">
							{SECTION_GROUPS.map((group) => (
								<div key={group.title} className="mb-4 last:mb-0">
									<h3 className="mb-2 px-4 font-bold text-base-content/40 text-xs uppercase tracking-widest">
										{group.title}
									</h3>
									<ul className="menu menu-md gap-1 p-0">
										{group.sections.map((key) => {
											const s = CONFIG_SECTIONS[key as ConfigSection | "system"];
											if (s.hidden) return null;
											const Icon = getIconComponent(s.icon);
											const isActive = activeSection === key;
											return (
												<li key={key}>
													<button
														type="button"
														aria-current={isActive ? "page" : undefined}
														className={`flex items-center gap-3 rounded-xl py-3 pr-4 pl-6 transition-all ${
															isActive
																? "scale-[1.02] bg-primary font-bold text-primary-content shadow-lg shadow-primary/20"
																: "text-base-content/70 hover:bg-base-200"
														}`}
														onClick={() => navigate(`/config/${key}`)}
													>
														<Icon className={`h-5 w-5 ${isActive ? "" : "text-base-content/40"}`} />
														<div className="min-w-0 flex-1 text-left">
															<div className="text-sm">{s.title}</div>
														</div>
														{!s.canEdit && (
															<span className="badge badge-ghost badge-xs text-base-content/70">
																🔒
															</span>
														)}
													</button>
												</li>
											);
										})}
									</ul>
								</div>
							))}
						</div>
					</div>
				</div>

				{/* Modern Content Card — min-w-0 lets grid column shrink so long inputs don’t overflow on narrow viewports */}
				<div className="min-w-0 lg:col-span-9 xl:col-span-10">
					{" "}
					<div className="card min-h-[600px] overflow-hidden rounded-2xl border-2 border-base-300/50 bg-base-100 shadow-md">
						<div className="card-body p-4 sm:p-10">
							{/* Modern Section Header */}
							<div className="mb-10 border-base-200 border-b pb-8">
								<div className="mb-2 flex items-start space-x-5 sm:items-center">
									<div className="shrink-0 rounded-2xl bg-primary/10 p-4 shadow-inner">
										{(() => {
											const Icon = getIconComponent(CONFIG_SECTIONS[activeSection].icon);
											return <Icon className="h-8 w-8 text-primary" />;
										})()}
									</div>
									<div className="min-w-0 flex-1">
										<h2 className="break-words font-bold text-3xl text-base-content tracking-tight">
											{CONFIG_SECTIONS[activeSection].title}
										</h2>
										<p className="mt-1 max-w-2xl break-words text-base-content/60 text-sm leading-relaxed">
											{CONFIG_SECTIONS[activeSection].description}
										</p>
									</div>
								</div>
							</div>

							<div
								className={`mx-auto w-full min-w-0 ${activeSection === "providers" ? "max-w-none" : "max-w-4xl"}`}
							>
								{activeSection === "auth" && (
									<AuthConfigSection
										config={config}
										onUpdate={handleConfigUpdate}
										isUpdating={updateConfigSection.isPending}
									/>
								)}
								{activeSection === "import" && (
									<ImportConfigSection
										config={config}
										onUpdate={handleConfigUpdate}
										isUpdating={updateConfigSection.isPending}
									/>
								)}
								{activeSection === "metadata" && (
									<MetadataConfigSection
										config={config}
										onUpdate={handleConfigUpdate}
										isUpdating={updateConfigSection.isPending}
									/>
								)}
								{activeSection === "streaming" && (
									<StreamingConfigSection
										config={config}
										onUpdate={handleConfigUpdate}
										isUpdating={updateConfigSection.isPending}
									/>
								)}
								{activeSection === "transcoding" && (
									<TranscodingConfigSection
										config={config}
										onUpdate={handleConfigUpdate}
										isUpdating={updateConfigSection.isPending}
									/>
								)}
								{activeSection === "system" && (
									<SystemConfigSection
										config={config}
										onUpdate={handleConfigUpdate}
										onRefresh={async () => {
											await refetch();
										}}
										isUpdating={updateConfigSection.isPending}
									/>
								)}
								{activeSection === "providers" && (
									<ProvidersConfigSection
										config={config}
										onUpdate={handleConfigUpdate}
										isUpdating={updateConfigSection.isPending}
									/>
								)}
								{activeSection === "sabnzbd" && (
									<SABnzbdConfigSection
										config={config}
										onUpdate={handleConfigUpdate}
										isUpdating={updateConfigSection.isPending}
									/>
								)}
								{activeSection === "arrs" && (
									<ArrsConfigSection
										config={config}
										onUpdate={handleConfigUpdate}
										isUpdating={updateConfigSection.isPending}
									/>
								)}
								{activeSection === "health" && (
									<HealthConfigSection
										config={config}
										onUpdate={handleConfigUpdate}
										isUpdating={updateConfigSection.isPending}
									/>
								)}
								{activeSection === "newznab" && (
									<NewznabConfigSection
										config={config}
										onUpdate={handleConfigUpdate}
										isUpdating={updateConfigSection.isPending}
									/>
								)}
								{activeSection === "local_media" && (
									<LocalMediaConfigSection
										config={config}
										onUpdate={handleConfigUpdate}
										isUpdating={updateConfigSection.isPending}
									/>
								)}
								{activeSection === "players" && (
									<TaterPlayersConfigSection
										config={config}
										onRefresh={async () => {
											await refetch();
										}}
									/>
								)}
								{![
									"auth",
									"import",
									"metadata",
									"streaming",
									"transcoding",
									"system",
									"providers",
									"sabnzbd",
									"arrs",
									"health",
									"newznab",
									"players",
								].includes(activeSection) && (
									<ComingSoonSection
										sectionName={CONFIG_SECTIONS[activeSection]?.title || activeSection}
									/>
								)}
							</div>
						</div>
					</div>
				</div>
			</div>
		</div>
	);
}
