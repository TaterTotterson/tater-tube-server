import {
	Check,
	KeyRound,
	Link2,
	MonitorSmartphone,
	Pencil,
	Plus,
	RefreshCw,
	ShieldOff,
	Tv,
	Unlink,
	Wifi,
	WifiOff,
	X,
} from "lucide-react";
import { useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import { useConfirm } from "../../contexts/ModalContext";
import { useToast } from "../../contexts/ToastContext";
import type {
	ConfigResponse,
	TaterPairingCodeCreateResponse,
	TaterPlayersConfig,
} from "../../types/config";

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
	const [players, setPlayers] = useState<TaterPlayersConfig>(
		config.players ?? { players: [], pairing_codes: [] },
	);
	const [pairingCode, setPairingCode] = useState<TaterPairingCodeCreateResponse | null>(null);
	const [newPlayerName, setNewPlayerName] = useState("");
	const [editingPlayerID, setEditingPlayerID] = useState("");
	const [editingName, setEditingName] = useState("");
	const [isLoading, setIsLoading] = useState(false);
	const [isCreating, setIsCreating] = useState(false);
	const [isRenaming, setIsRenaming] = useState(false);
	const [linkingPlayerID, setLinkingPlayerID] = useState("");

	useEffect(() => {
		setPlayers((current) => {
			const next = config.players ?? { players: [], pairing_codes: [] };
			return { ...next, displays: next.displays ?? current.displays };
		});
	}, [config.players]);

	useEffect(() => {
		let cancelled = false;
		const pollDisplays = () => {
			void apiClient
				.getTaterPlayers()
				.then((next) => {
					if (!cancelled) setPlayers(next);
				})
				.catch(() => {
					// Keep the current cards stable through a brief server interruption.
				});
		};
		pollDisplays();
		const timer = window.setInterval(pollDisplays, 5000);
		return () => {
			cancelled = true;
			window.clearInterval(timer);
		};
	}, []);

	useEffect(() => {
		if (!pairingCode) return;

		let cancelled = false;
		let timeout: ReturnType<typeof setTimeout> | undefined;
		const poll = async () => {
			try {
				const nextPlayers = await apiClient.getTaterPlayers();
				if (cancelled) return;
				setPlayers(nextPlayers);

				const stillWaiting = nextPlayers.pairing_codes.some((item) => item.id === pairingCode.id);
				if (!stillWaiting) {
					setPairingCode(null);
					const expiresAt = new Date(pairingCode.expires_at).getTime();
					if (!Number.isFinite(expiresAt) || expiresAt > Date.now()) {
						showToast({
							type: "success",
							title: "Player Connected",
							message: `${pairingCode.name || "Tater Tube Player"} is ready.`,
						});
					}
					await onRefresh?.();
					return;
				}
			} catch {
				// Pairing can survive a brief network interruption; keep waiting quietly.
			}
			if (!cancelled) timeout = setTimeout(poll, 1250);
		};

		timeout = setTimeout(poll, 600);
		return () => {
			cancelled = true;
			if (timeout) clearTimeout(timeout);
		};
	}, [onRefresh, pairingCode, showToast]);

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
			const code = await apiClient.createTaterPairingCode(newPlayerName.trim());
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

	const startRename = (id: string, name: string) => {
		setEditingPlayerID(id);
		setEditingName(name || "Tater Tube Player");
	};

	const cancelRename = () => {
		setEditingPlayerID("");
		setEditingName("");
	};

	const rename = async (id: string) => {
		const name = editingName.trim();
		if (!name) {
			showToast({
				type: "error",
				title: "Name Required",
				message: "Enter a player name before saving.",
			});
			return;
		}
		setIsRenaming(true);
		try {
			await apiClient.renameTaterPlayer(id, name);
			cancelRename();
			await refresh();
			showToast({
				type: "success",
				title: "Player Renamed",
				message: "The player name was updated.",
			});
		} catch (error) {
			showToast({
				type: "error",
				title: "Rename Failed",
				message: error instanceof Error ? error.message : "Unable to rename player.",
			});
		} finally {
			setIsRenaming(false);
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
			showToast({
				type: "success",
				title: "Player Revoked",
				message: "The player token was disabled.",
			});
		} catch (error) {
			showToast({
				type: "error",
				title: "Revoke Failed",
				message: error instanceof Error ? error.message : "Unable to revoke player.",
			});
		}
	};

	const updateDisplayLink = async (playerID: string, displayID: string) => {
		setLinkingPlayerID(playerID);
		try {
			await apiClient.linkTaterPlayerDisplay(playerID, displayID);
			await refresh();
			showToast({
				type: "success",
				title: displayID ? "Display Linked" : "Display Unlinked",
				message: displayID
					? "Now-playing details will be sent directly to the selected screen."
					: "This player no longer controls a companion display.",
			});
		} catch (error) {
			showToast({
				type: "error",
				title: "Display Link Failed",
				message: error instanceof Error ? error.message : "Unable to update the companion display.",
			});
		} finally {
			setLinkingPlayerID("");
		}
	};

	const activePlayers = players.players.filter((player) => !player.revoked_at);
	const displays = players.displays ?? [];
	const compatibleDisplays = displays.filter((display) => display.online && display.compatible);

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
							Create a short-lived PIN here, then enter this server URL and the PIN on the Tater
							Tube player.
						</p>
					</div>
					<div className="flex gap-2">
						<input
							type="text"
							className="input input-bordered input-sm w-48"
							value={newPlayerName}
							onChange={(event) => setNewPlayerName(event.target.value)}
							placeholder="Living Room"
							maxLength={48}
						/>
						<button
							type="button"
							className="btn btn-outline btn-sm"
							onClick={refresh}
							disabled={isLoading}
						>
							{isLoading ? (
								<span className="loading loading-spinner loading-xs" />
							) : (
								<RefreshCw className="h-4 w-4" />
							)}
							Refresh
						</button>
						<button
							type="button"
							className="btn btn-primary btn-sm"
							onClick={createCode}
							disabled={isCreating}
						>
							{isCreating ? (
								<span className="loading loading-spinner loading-xs" />
							) : (
								<Plus className="h-4 w-4" />
							)}
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
						<div className="font-black font-mono text-5xl text-primary tracking-[0.18em]">
							{pairingCode.code}
						</div>
						<div className="mt-2 font-bold text-base-content/70 text-sm">
							{pairingCode.name || "Tater Tube Player"}
						</div>
						<p className="mt-3 text-base-content/60 text-xs">
							Expires {formatDate(pairingCode.expires_at)}.
						</p>
						<div className="mt-3 flex items-center gap-2 text-primary text-xs">
							<span className="loading loading-spinner loading-xs" />
							Waiting for the player to connect…
						</div>
					</div>
				)}
			</div>

			<div className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="mb-5 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
					<div>
						<div className="flex items-center gap-2">
							<MonitorSmartphone className="h-4 w-4 text-primary" />
							<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
								Players & Companion Displays
							</h4>
						</div>
						<p className="mt-2 max-w-2xl text-base-content/60 text-sm">
							Choose the LAN display that should show artwork, title, and progress for each player.
							Screens are discovered directly, without Tater or Home Assistant.
						</p>
					</div>
					<div className="badge badge-lg gap-2 border-primary/25 bg-primary/10 text-primary">
						<Wifi className="h-3.5 w-3.5" />
						{compatibleDisplays.length} screen{compatibleDisplays.length === 1 ? "" : "s"} ready
					</div>
				</div>

				<div className="space-y-3">
					{activePlayers.length === 0 && (
						<div className="rounded-xl border border-base-300 bg-base-100/70 p-4 text-base-content/60 text-sm">
							No active players paired.
						</div>
					)}
					{activePlayers.map((player) => {
						const linkedValue = player.linked_display_id ?? "";
						const linkedDisplay = displays.find((display) => display.id === linkedValue);
						const choices = compatibleDisplays.filter(
							(display) => !display.linked_player_id || display.linked_player_id === player.id,
						);
						const linkedChoiceAvailable = choices.some((display) => display.id === linkedValue);
						const linkedReady = Boolean(linkedDisplay?.online && linkedDisplay.compatible);
						return (
							<div
								key={player.id}
								className="overflow-hidden rounded-2xl border border-base-300 bg-base-100/80 shadow-sm"
							>
								<div className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
									<div className="min-w-0">
										{editingPlayerID === player.id ? (
											<input
												type="text"
												className="input input-bordered input-sm w-full max-w-xs font-bold"
												value={editingName}
												onChange={(event) => setEditingName(event.target.value)}
												onKeyDown={(event) => {
													if (event.key === "Enter") void rename(player.id);
													if (event.key === "Escape") cancelRename();
												}}
												maxLength={48}
											/>
										) : (
											<div className="truncate font-bold">{player.name || "Tater Tube Player"}</div>
										)}
										<div className="mt-1 text-base-content/50 text-xs">
											Last seen {formatDate(player.last_seen_at)} · Paired{" "}
											{formatDate(player.created_at)}
										</div>
									</div>
									<div className="flex flex-wrap gap-2">
										{editingPlayerID === player.id ? (
											<>
												<button
													type="button"
													className="btn btn-primary btn-sm"
													onClick={() => rename(player.id)}
													disabled={isRenaming}
												>
													{isRenaming ? (
														<span className="loading loading-spinner loading-xs" />
													) : (
														<Check className="h-4 w-4" />
													)}
													Save
												</button>
												<button
													type="button"
													className="btn btn-outline btn-sm"
													onClick={cancelRename}
													disabled={isRenaming}
												>
													<X className="h-4 w-4" />
													Cancel
												</button>
											</>
										) : (
											<button
												type="button"
												className="btn btn-outline btn-sm"
												onClick={() => startRename(player.id, player.name)}
											>
												<Pencil className="h-4 w-4" />
												Rename
											</button>
										)}
										<button
											type="button"
											className="btn btn-error btn-outline btn-sm"
											onClick={() => revoke(player.id, player.name)}
										>
											<ShieldOff className="h-4 w-4" />
											Revoke
										</button>
									</div>
								</div>

								<div className="border-base-300 border-t bg-gradient-to-r from-primary/8 via-base-200/60 to-base-200/20 p-4">
									<div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_minmax(260px,380px)] md:items-center">
										<div className="flex min-w-0 items-start gap-3">
											<div className="rounded-xl border border-primary/20 bg-primary/10 p-2.5 text-primary">
												{linkedValue ? (
													<Link2 className="h-5 w-5" />
												) : (
													<Unlink className="h-5 w-5" />
												)}
											</div>
											<div className="min-w-0">
												<div className="font-bold text-sm">Companion now-playing screen</div>
												<div className="mt-1 flex items-center gap-1.5 text-base-content/55 text-xs">
													{linkedReady ? (
														<Wifi className="h-3.5 w-3.5 text-success" />
													) : (
														<WifiOff className="h-3.5 w-3.5" />
													)}
													{linkedDisplay
														? `${linkedDisplay.name}${linkedDisplay.room ? ` · ${linkedDisplay.room}` : ""}${!linkedDisplay.compatible ? " · firmware update required" : !linkedDisplay.online ? " · offline" : ""}`
														: linkedValue
															? "Saved screen is currently offline"
															: "Not linked — playback stays on the TV only"}
												</div>
											</div>
										</div>
										<div className="relative">
											<select
												className="select select-bordered w-full bg-base-100 pr-10 font-medium"
												value={linkedValue}
												onChange={(event) => void updateDisplayLink(player.id, event.target.value)}
												disabled={linkingPlayerID === player.id}
											>
												<option value="">No companion display</option>
												{linkedValue && !linkedChoiceAvailable && (
													<option value={linkedValue}>
														{linkedDisplay
															? `${linkedDisplay.name} (${linkedDisplay.compatible ? "offline" : "update required"})`
															: "Saved screen (offline)"}
													</option>
												)}
												{choices.map((display) => (
													<option key={display.id} value={display.id}>
														{display.name}
														{display.room ? ` · ${display.room}` : ""} — {display.board}
													</option>
												))}
											</select>
											{linkingPlayerID === player.id && (
												<span className="loading loading-spinner loading-sm -translate-y-1/2 absolute top-1/2 right-3" />
											)}
										</div>
									</div>
								</div>
							</div>
						);
					})}
				</div>
			</div>
		</div>
	);
}
