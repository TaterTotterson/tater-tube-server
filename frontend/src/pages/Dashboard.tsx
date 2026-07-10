import { Check, Copy, List, Radio, Settings, Tv, Wifi } from "lucide-react";
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useToast } from "../contexts/ToastContext";
import { usePoolMetrics, useQueueStats } from "../hooks/useApi";
import { useConfig } from "../hooks/useConfig";
import { copyToClipboard } from "../lib/utils";

function StatusCard({
	icon: Icon,
	label,
	value,
	helper,
}: {
	icon: typeof Tv;
	label: string;
	value: string;
	helper: string;
}) {
	return (
		<div className="card border-base-300 bg-base-200/70">
			<div className="card-body gap-3 p-5">
				<div className="flex items-center gap-3">
					<div className="rounded-md bg-primary/15 p-2">
						<Icon className="h-5 w-5 text-primary" />
					</div>
					<div className="min-w-0">
						<div className="text-base-content/50 text-xs uppercase tracking-widest">{label}</div>
						<div className="truncate font-bold text-xl">{value}</div>
					</div>
				</div>
				<p className="text-base-content/60 text-sm">{helper}</p>
			</div>
		</div>
	);
}

export function Dashboard() {
	const { data: config } = useConfig();
	const { data: queueStats } = useQueueStats();
	const { data: poolMetrics } = usePoolMetrics();
	const { showToast } = useToast();
	const [copied, setCopied] = useState(false);

	const addonURL = useMemo(() => {
		if (!config?.stremio?.enabled || !config.download_key) return "";
		const baseURL = (config.stremio.base_url || window.location.origin).replace(/\/$/, "");
		return `${baseURL}/stremio/${config.download_key}/manifest.json`;
	}, [config]);

	const activeQueueCount = queueStats
		? queueStats.total_processing +
			Math.max(0, queueStats.total_queued - queueStats.total_processing - queueStats.total_completed)
		: 0;

	const handleCopyAddonURL = async () => {
		if (!addonURL) return;
		const ok = await copyToClipboard(addonURL);
		setCopied(ok);
		showToast({
			type: ok ? "success" : "error",
			title: ok ? "Copied" : "Copy Failed",
			message: ok ? "Stremio addon URL copied." : "Unable to copy addon URL.",
		});
		if (ok) setTimeout(() => setCopied(false), 1800);
	};

	return (
		<div className="space-y-6">
			<section className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_260px]">
				<div className="card overflow-hidden border-primary/20 bg-base-200/70">
					<div className="card-body gap-6 p-6 sm:p-8">
						<div className="space-y-3">
							<div className="text-primary text-xs uppercase tracking-[0.25em]">
								Stremio Usenet Bridge
							</div>
							<h1 className="font-bold text-3xl leading-tight sm:text-4xl">
								Tater Tube Server
							</h1>
							<p className="max-w-2xl text-base-content/70">
								Add an NNTP provider, enable the Stremio endpoint, then use the addon URL in
								Stremio or apps that can submit NZBs to the stream endpoint.
							</p>
						</div>

						<div className="grid gap-3 sm:grid-cols-3">
							<Link to="/config/stremio" className="btn btn-primary">
								<Tv className="h-4 w-4" />
								Stremio Setup
							</Link>
							<Link to="/config/providers" className="btn btn-outline">
								<Radio className="h-4 w-4" />
								NNTP Providers
							</Link>
							<Link to="/config/system" className="btn btn-outline">
								<Settings className="h-4 w-4" />
								System
							</Link>
						</div>

						<div className="rounded-lg border border-base-300 bg-base-100/80 p-4">
							<div className="mb-2 flex items-center gap-2 text-base-content/60 text-xs uppercase tracking-widest">
								<Tv className="h-4 w-4 text-primary" />
								Addon URL
							</div>
							{addonURL ? (
								<div className="flex min-w-0 flex-wrap items-center gap-2">
									<code className="min-w-0 flex-1 truncate rounded bg-base-300 px-3 py-2 font-mono text-xs">
										{addonURL}
									</code>
									<button type="button" className="btn btn-sm btn-primary" onClick={handleCopyAddonURL}>
										{copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
										Copy
									</button>
								</div>
							) : (
								<p className="text-base-content/60 text-sm">
									Enable Stremio integration and generate an API key to show the install URL.
								</p>
							)}
						</div>
					</div>
				</div>

				<div className="card place-items-center border-primary/20 bg-base-200/70 p-4">
					<img
						src="/tater-tube-server-mascot.png"
						alt="Tater Tube Server mascot"
						className="max-h-64 w-full object-contain"
					/>
				</div>
			</section>

			<section className="grid gap-4 md:grid-cols-3">
				<StatusCard
					icon={Wifi}
					label="Stremio"
					value={config?.stremio?.enabled ? "Enabled" : "Disabled"}
					helper="Controls addon and NZB stream endpoints."
				/>
				<StatusCard
					icon={Radio}
					label="Providers"
					value={String(poolMetrics?.providers?.length ?? config?.providers?.length ?? 0)}
					helper="Configured NNTP backends available for streaming."
				/>
				<StatusCard
					icon={List}
					label="Queue"
					value={`${activeQueueCount} active`}
					helper={`${queueStats?.total_failed ?? 0} failed items currently need attention.`}
				/>
			</section>
		</div>
	);
}
