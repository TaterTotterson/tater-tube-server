import { List, Radio, Settings, Tv, Wifi } from "lucide-react";
import { Link } from "react-router-dom";
import { usePoolMetrics, useQueueStats } from "../hooks/useApi";
import { useConfig } from "../hooks/useConfig";

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

	const activeQueueCount = queueStats
		? queueStats.total_processing +
			Math.max(0, queueStats.total_queued - queueStats.total_processing - queueStats.total_completed)
		: 0;

	return (
		<div className="space-y-6">
			<section className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_260px]">
				<div className="card overflow-hidden border-primary/20 bg-base-200/70">
					<div className="card-body gap-6 p-6 sm:p-8">
						<div className="space-y-3">
							<div className="text-primary text-xs uppercase tracking-[0.25em]">
								Tater Tube Stream Server
							</div>
							<h1 className="font-bold text-3xl leading-tight sm:text-4xl">
								Tater Tube Server
							</h1>
							<p className="max-w-2xl text-base-content/70">
								Add an NNTP provider, configure the Newznab Stream catalog, then pair Tater Tube
								players with short-lived setup PINs.
							</p>
						</div>

						<div className="grid gap-3 sm:grid-cols-3">
							<Link to="/config/players" className="btn btn-primary">
								<Tv className="h-4 w-4" />
								Pair Players
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
								Player Setup
							</div>
							<p className="text-base-content/60 text-sm">
								Open Tater Tube Players, create a PIN, then enter this server URL and PIN on the player.
							</p>
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
					label="Players"
					value={String(config?.players?.players?.filter((player) => !player.revoked_at).length ?? 0)}
					helper="Paired Tater Tube players allowed to browse and stream."
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
