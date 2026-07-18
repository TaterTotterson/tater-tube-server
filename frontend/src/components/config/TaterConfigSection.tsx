import { Clock3, Eye, KeyRound, Plus, RefreshCw, ShieldOff, Sparkles, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import { useConfirm } from "../../contexts/ModalContext";
import { useToast } from "../../contexts/ToastContext";
import type {
	TaterAdminState,
	TaterPairingCodeCreateResponse,
	TaterViewingEvent,
} from "../../types/config";

const EMPTY_STATE: TaterAdminState = {
	connections: [],
	pairing_codes: [],
	viewing_events: [],
	recommendation_batches: [],
	active_recommendations: { items: [] },
};

function formatDate(value?: string) {
	if (!value) return "Never";
	const date = new Date(value);
	return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function formatProgress(event: TaterViewingEvent) {
	if (!event.duration_ms) return event.state;
	const percent = Math.min(100, Math.round((event.position_ms / event.duration_ms) * 100));
	return `${event.state} · ${percent}%`;
}

export function TaterConfigSection() {
	const { showToast } = useToast();
	const { confirmAction } = useConfirm();
	const [state, setState] = useState<TaterAdminState>(EMPTY_STATE);
	const [coreName, setCoreName] = useState("Tater");
	const [pairingCode, setPairingCode] = useState<TaterPairingCodeCreateResponse | null>(null);
	const [loading, setLoading] = useState(true);
	const [creating, setCreating] = useState(false);

	const refresh = useCallback(async () => {
		setLoading(true);
		try {
			setState(await apiClient.getTaterAdminState());
		} catch (error) {
			showToast({
				type: "error",
				title: "Tater Refresh Failed",
				message: error instanceof Error ? error.message : "Unable to load Tater state.",
			});
		} finally {
			setLoading(false);
		}
	}, [showToast]);

	useEffect(() => {
		void refresh();
	}, [refresh]);

	const createCode = async () => {
		setCreating(true);
		try {
			const code = await apiClient.createTaterCorePairingCode(coreName.trim());
			setPairingCode(code);
			await refresh();
		} catch (error) {
			showToast({
				type: "error",
				title: "PIN Failed",
				message: error instanceof Error ? error.message : "Unable to create a pairing PIN.",
			});
		} finally {
			setCreating(false);
		}
	};

	const revoke = async (id: string, name: string) => {
		const confirmed = await confirmAction(
			"Disconnect Tater Core",
			`Disconnect ${name || "this Tater Core"}? It will need a new PIN to reconnect.`,
			{ type: "error", confirmText: "Disconnect", confirmButtonClass: "btn-error" },
		);
		if (!confirmed) return;
		try {
			await apiClient.revokeTaterCore(id);
			await refresh();
			showToast({ type: "success", title: "Tater Disconnected" });
		} catch (error) {
			showToast({
				type: "error",
				title: "Disconnect Failed",
				message: error instanceof Error ? error.message : "Unable to disconnect Tater Core.",
			});
		}
	};

	const clearHistory = async () => {
		const confirmed = await confirmAction(
			"Clear Viewing History",
			"Clear the viewing context used by Tater for prompts and recommendations?",
			{ type: "error", confirmText: "Clear History", confirmButtonClass: "btn-error" },
		);
		if (!confirmed) return;
		try {
			await apiClient.clearTaterViewingHistory();
			await refresh();
			showToast({ type: "success", title: "Viewing History Cleared" });
		} catch (error) {
			showToast({
				type: "error",
				title: "Clear Failed",
				message: error instanceof Error ? error.message : "Unable to clear viewing history.",
			});
		}
	};

	const activeConnections = state.connections.filter((item) => !item.revoked_at);
	const recommendations = state.active_recommendations?.items ?? [];
	const batch = state.active_recommendations?.batch;

	return (
		<div className="min-w-0 space-y-8">
			<div className="rounded-2xl border-2 border-primary/25 bg-primary/5 p-6">
				<div className="flex flex-col gap-5 sm:flex-row sm:items-start sm:justify-between">
					<div className="max-w-2xl">
						<div className="mb-3 flex items-center gap-2">
							<Sparkles className="h-5 w-5 text-primary" />
							<h3 className="font-black text-lg">Connect Tater Core</h3>
						</div>
						<p className="text-base-content/65 text-sm leading-relaxed">
							Create a short-lived PIN, then enter this server address and the PIN in the Tater Tube
							Core settings. Core receives a dedicated token that can read viewing context and
							publish recommendations, but cannot stream media or administer the server.
						</p>
						<div className="mt-3 rounded-lg bg-base-100/80 px-3 py-2 font-mono text-xs">
							{window.location.origin}
						</div>
					</div>
					<div className="flex flex-wrap gap-2">
						<input
							className="input input-bordered input-sm w-40"
							value={coreName}
							onChange={(event) => setCoreName(event.target.value)}
							placeholder="Tater"
							maxLength={48}
						/>
						<button
							type="button"
							className="btn btn-primary btn-sm"
							onClick={createCode}
							disabled={creating}
						>
							{creating ? (
								<span className="loading loading-spinner loading-xs" />
							) : (
								<Plus className="h-4 w-4" />
							)}
							Add Tater
						</button>
						<button
							type="button"
							className="btn btn-outline btn-sm"
							onClick={refresh}
							disabled={loading}
						>
							{loading ? (
								<span className="loading loading-spinner loading-xs" />
							) : (
								<RefreshCw className="h-4 w-4" />
							)}
							Refresh
						</button>
					</div>
				</div>

				{pairingCode && (
					<div className="mt-6 rounded-xl border border-primary/30 bg-base-100 p-5 shadow-sm">
						<div className="mb-2 flex items-center gap-2 font-bold text-primary text-xs uppercase tracking-widest">
							<KeyRound className="h-4 w-4" />
							Tater Core Pairing PIN
						</div>
						<div className="font-black font-mono text-5xl text-primary tracking-[0.18em]">
							{pairingCode.code}
						</div>
						<p className="mt-3 text-base-content/55 text-xs">
							For {pairingCode.name || "Tater Tube Core"} · Expires{" "}
							{formatDate(pairingCode.expires_at)}
						</p>
					</div>
				)}
			</div>

			<div className="grid gap-4 md:grid-cols-3">
				<div className="stat rounded-2xl border border-base-300 bg-base-200/50">
					<div className="stat-figure text-primary">
						<Sparkles className="h-7 w-7" />
					</div>
					<div className="stat-title">Connected Taters</div>
					<div className="stat-value text-primary">{activeConnections.length}</div>
				</div>
				<div className="stat rounded-2xl border border-base-300 bg-base-200/50">
					<div className="stat-figure text-secondary">
						<Eye className="h-7 w-7" />
					</div>
					<div className="stat-title">Recent Watch Events</div>
					<div className="stat-value text-secondary">{state.viewing_events.length}</div>
				</div>
				<div className="stat rounded-2xl border border-base-300 bg-base-200/50">
					<div className="stat-figure text-accent">
						<Clock3 className="h-7 w-7" />
					</div>
					<div className="stat-title">Active Picks</div>
					<div className="stat-value text-accent">{recommendations.length}</div>
				</div>
			</div>

			<section className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<h4 className="mb-4 font-bold text-base-content/45 text-xs uppercase tracking-widest">
					Connected Taters
				</h4>
				<div className="space-y-3">
					{activeConnections.length === 0 && (
						<div className="rounded-xl border border-base-300 bg-base-100/70 p-4 text-base-content/55 text-sm">
							No Tater Core is connected yet.
						</div>
					)}
					{activeConnections.map((item) => (
						<div
							key={item.id}
							className="flex items-center justify-between gap-4 rounded-xl border border-base-300 bg-base-100/80 p-4"
						>
							<div className="min-w-0">
								<div className="truncate font-bold">{item.name}</div>
								<div className="mt-1 text-base-content/50 text-xs">
									Last seen {formatDate(item.last_seen_at)} · Paired {formatDate(item.created_at)}
								</div>
							</div>
							<button
								type="button"
								className="btn btn-error btn-outline btn-sm"
								onClick={() => revoke(item.id, item.name)}
							>
								<ShieldOff className="h-4 w-4" />
								Disconnect
							</button>
						</div>
					))}
				</div>
			</section>

			<section className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="mb-4 flex items-center justify-between gap-3">
					<div>
						<h4 className="font-bold text-base-content/45 text-xs uppercase tracking-widest">
							Tater&apos;s Picks
						</h4>
						{batch && (
							<p className="mt-2 text-base-content/55 text-xs">
								Fresh until {formatDate(batch.expires_at)}
							</p>
						)}
					</div>
				</div>
				<div className="grid gap-3 sm:grid-cols-2">
					{recommendations.length === 0 && (
						<div className="col-span-full rounded-xl border border-base-300 bg-base-100/70 p-4 text-base-content/55 text-sm">
							No active recommendations. Once Core has viewing context, it will send a fresh batch
							here.
						</div>
					)}
					{recommendations.map((item) => (
						<div key={item.id} className="rounded-xl border border-base-300 bg-base-100/80 p-4">
							<div className="flex items-start justify-between gap-3">
								<div className="font-bold">{item.title}</div>
								<span className="badge badge-outline badge-sm">{item.media_type}</span>
							</div>
							<p className="mt-2 text-base-content/60 text-sm leading-relaxed">{item.reason}</p>
							{item.feedback && (
								<div className="mt-3 text-primary text-xs uppercase">{item.feedback}</div>
							)}
						</div>
					))}
				</div>
			</section>

			<section className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="mb-4 flex items-center justify-between gap-3">
					<h4 className="font-bold text-base-content/45 text-xs uppercase tracking-widest">
						Recent Viewing Context
					</h4>
					<button
						type="button"
						className="btn btn-error btn-outline btn-sm"
						onClick={clearHistory}
						disabled={!state.viewing_events.length}
					>
						<Trash2 className="h-4 w-4" />
						Clear
					</button>
				</div>
				<div className="max-h-[34rem] space-y-2 overflow-y-auto pr-1">
					{state.viewing_events.length === 0 && (
						<div className="rounded-xl border border-base-300 bg-base-100/70 p-4 text-base-content/55 text-sm">
							No watch events have arrived from a player yet.
						</div>
					)}
					{state.viewing_events.map((event) => (
						<div
							key={event.event_id}
							className="flex items-center justify-between gap-4 rounded-xl border border-base-300 bg-base-100/80 px-4 py-3"
						>
							<div className="min-w-0">
								<div className="truncate font-semibold">{event.title}</div>
								<div className="mt-1 text-base-content/50 text-xs">
									{event.source} · {event.media_type} · {formatDate(event.occurred_at)}
								</div>
							</div>
							<span className="badge badge-ghost whitespace-nowrap">{formatProgress(event)}</span>
						</div>
					))}
				</div>
			</section>
		</div>
	);
}
