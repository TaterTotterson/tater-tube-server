import { KeyRound, Plus, RefreshCw, ShieldOff, Tv } from "lucide-react";
import { useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import { useConfirm } from "../../contexts/ModalContext";
import { useToast } from "../../contexts/ToastContext";
import type { ConfigResponse, TaterPairingCodeCreateResponse, TaterPlayersConfig } from "../../types/config";

interface TaterPlayersConfigSectionProps {
	config: ConfigResponse;
	onRefresh?: () => Promise<void>;
}

function formatDate(value?: string) {
	if (!value) return "Never";
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return value;
	return date.toLocaleString();
}

export function TaterPlayersConfigSection({ config, onRefresh }: TaterPlayersConfigSectionProps) {
	const { showToast } = useToast();
	const { confirmAction } = useConfirm();
	const [players, setPlayers] = useState<TaterPlayersConfig>(config.players ?? { players: [], pairing_codes: [] });
	const [pairingCode, setPairingCode] = useState<TaterPairingCodeCreateResponse | null>(null);
	const [isLoading, setIsLoading] = useState(false);
	const [isCreating, setIsCreating] = useState(false);

	useEffect(() => {
		setPlayers(config.players ?? { players: [], pairing_codes: [] });
	}, [config.players]);

	const refresh = async () => {
		setIsLoading(true);
		try {
			const data = await apiClient.getTaterPlayers();
			setPlayers(data);
			await onRefresh?.();
		} catch (error) {
			showToast({
				type: "error",
				title: "Refresh Failed",
				message: error instanceof Error ? error.message : "Unable to load players.",
			});
		} finally {
			setIsLoading(false);
		}
	};

	const createCode = async () => {
		setIsCreating(true);
		try {
			const code = await apiClient.createTaterPairingCode("Tater Tube Player");
			setPairingCode(code);
			await refresh();
		} catch (error) {
			showToast({
				type: "error",
				title: "Code Failed",
				message: error instanceof Error ? error.message : "Unable to create pairing code.",
			});
		} finally {
			setIsCreating(false);
		}
	};

	const revoke = async (id: string, name: string) => {
		const confirmed = await confirmAction(
			"Revoke Player",
			`Revoke ${name || "this player"}? It will need to be paired again before it can stream.`,
			{ type: "error", confirmText: "Revoke", confirmButtonClass: "btn-error" },
		);
		if (!confirmed) return;
		try {
			await apiClient.revokeTaterPlayer(id);
			await refresh();
			showToast({ type: "success", title: "Player Revoked", message: "The player token was disabled." });
		} catch (error) {
			showToast({
				type: "error",
				title: "Revoke Failed",
				message: error instanceof Error ? error.message : "Unable to revoke player.",
			});
		}
	};

	const activePlayers = players.players.filter((player) => !player.revoked_at);
	const revokedPlayers = players.players.filter((player) => player.revoked_at);

	return (
		<div className="min-w-0 space-y-8">
			<div className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
					<div className="min-w-0">
						<div className="mb-3 flex items-center gap-2">
							<Tv className="h-4 w-4 text-base-content/60" />
							<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
								Player Pairing
							</h4>
						</div>
						<p className="max-w-2xl text-base-content/60 text-sm leading-relaxed">
							Create a short-lived PIN here, then enter this server URL and the PIN on the Tater Tube player.
						</p>
					</div>
					<div className="flex gap-2">
						<button type="button" className="btn btn-outline btn-sm" onClick={refresh} disabled={isLoading}>
							{isLoading ? <span className="loading loading-spinner loading-xs" /> : <RefreshCw className="h-4 w-4" />}
							Refresh
						</button>
						<button type="button" className="btn btn-primary btn-sm" onClick={createCode} disabled={isCreating}>
							{isCreating ? <span className="loading loading-spinner loading-xs" /> : <Plus className="h-4 w-4" />}
							Add Player
						</button>
					</div>
				</div>

				{pairingCode && (
					<div className="mt-6 rounded-xl border border-primary/30 bg-primary/10 p-5">
						<div className="mb-2 flex items-center gap-2 text-primary text-xs uppercase tracking-widest">
							<KeyRound className="h-4 w-4" />
							Pairing PIN
						</div>
						<div className="font-mono font-black text-5xl tracking-[0.18em] text-primary">
							{pairingCode.code}
						</div>
						<p className="mt-3 text-base-content/60 text-xs">
							Expires {formatDate(pairingCode.expires_at)}.
						</p>
					</div>
				)}
			</div>

			<div className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="mb-5 flex items-center gap-2">
					<Tv className="h-4 w-4 text-base-content/60" />
					<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
						Paired Players
					</h4>
				</div>

				<div className="space-y-3">
					{activePlayers.length === 0 && (
						<div className="rounded-xl border border-base-300 bg-base-100/70 p-4 text-base-content/60 text-sm">
							No active players paired.
						</div>
					)}
					{activePlayers.map((player) => (
						<div key={player.id} className="flex flex-col gap-3 rounded-xl border border-base-300 bg-base-100/70 p-4 sm:flex-row sm:items-center sm:justify-between">
							<div className="min-w-0">
								<div className="truncate font-bold">{player.name || "Tater Tube Player"}</div>
								<div className="mt-1 text-base-content/50 text-xs">
									Last seen {formatDate(player.last_seen_at)} · Paired {formatDate(player.created_at)}
								</div>
							</div>
							<button type="button" className="btn btn-error btn-outline btn-sm" onClick={() => revoke(player.id, player.name)}>
								<ShieldOff className="h-4 w-4" />
								Revoke
							</button>
						</div>
					))}
				</div>

				{revokedPlayers.length > 0 && (
					<div className="mt-6">
						<h5 className="mb-2 font-bold text-base-content/40 text-xs uppercase tracking-widest">
							Revoked
						</h5>
						<div className="space-y-2">
							{revokedPlayers.map((player) => (
								<div key={player.id} className="rounded-xl border border-base-300 bg-base-100/40 p-3 text-base-content/50 text-sm">
									{player.name || "Tater Tube Player"} · Revoked {formatDate(player.revoked_at)}
								</div>
							))}
						</div>
					</div>
				)}
			</div>
		</div>
	);
}
