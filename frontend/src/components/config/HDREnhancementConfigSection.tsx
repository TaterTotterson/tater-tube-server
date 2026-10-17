import {
	AlertTriangle,
	CheckCircle2,
	Gauge,
	Info,
	RefreshCw,
	Save,
	ShieldCheck,
	Sparkles,
	Sun,
	Tv2,
} from "lucide-react";
import { useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import type {
	ConfigResponse,
	HDREnhancementCompatibilityDetection,
	HDREnhancementConfig,
} from "../../types/config";

interface HDREnhancementConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: HDREnhancementConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

const STRENGTHS = [
	{
		id: "conservative",
		label: "Conservative",
		badge: "Natural",
		description:
			"A restrained highlight lift with minimal color expansion for a subtle HDR presentation.",
		icon: ShieldCheck,
	},
	{
		id: "balanced",
		label: "Balanced",
		badge: "Recommended",
		description:
			"Brighter highlights and a wider color presentation without pushing the picture too far.",
		icon: Gauge,
	},
	{
		id: "vivid",
		label: "Vivid",
		badge: "Strongest",
		description:
			"The most noticeable highlight and color expansion for displays with extra HDR headroom.",
		icon: Sparkles,
	},
] as const;

function normalizeHDREnhancement(value?: HDREnhancementConfig): HDREnhancementConfig {
	const strength = value?.strength;
	return {
		enabled: value?.enabled === true,
		strength: strength === "conservative" || strength === "vivid" ? strength : "balanced",
	};
}

export function HDREnhancementConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: HDREnhancementConfigSectionProps) {
	const [data, setData] = useState<HDREnhancementConfig>(() =>
		normalizeHDREnhancement(config.hdr_enhancement),
	);
	const [hasChanges, setHasChanges] = useState(false);
	const [compatibility, setCompatibility] = useState<HDREnhancementCompatibilityDetection | null>(
		null,
	);
	const [isDetecting, setIsDetecting] = useState(false);
	const [detectionError, setDetectionError] = useState("");

	useEffect(() => {
		setData(normalizeHDREnhancement(config.hdr_enhancement));
		setHasChanges(false);
	}, [config.hdr_enhancement]);

	const updateData = (next: HDREnhancementConfig) => {
		const normalized = normalizeHDREnhancement(next);
		setData(normalized);
		setHasChanges(
			JSON.stringify(normalized) !==
				JSON.stringify(normalizeHDREnhancement(config.hdr_enhancement)),
		);
	};

	const handleSave = async () => {
		if (!onUpdate || !hasChanges) return;
		await onUpdate("hdr_enhancement", data);
		setHasChanges(false);
	};

	const runDetection = async () => {
		setIsDetecting(true);
		setDetectionError("");
		try {
			setCompatibility(await apiClient.detectHDREnhancementCompatibility());
		} catch (error) {
			setDetectionError(error instanceof Error ? error.message : "HDR detection failed");
		} finally {
			setIsDetecting(false);
		}
	};

	return (
		<div className="space-y-6">
			<div className="relative overflow-hidden rounded-2xl border border-warning/30 bg-gradient-to-br from-warning/15 via-base-200/70 to-primary/5 p-5 sm:p-6">
				<div className="-right-14 -top-16 absolute h-48 w-48 rounded-full bg-warning/15 blur-3xl" />
				<div className="relative flex flex-col gap-5 sm:flex-row sm:items-center sm:justify-between">
					<div className="flex min-w-0 gap-4">
						<div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl border border-warning/30 bg-warning/15 text-warning">
							<Sun className="h-6 w-6" />
						</div>
						<div>
							<div className="flex flex-wrap items-center gap-2">
								<h3 className="font-bold text-xl">SDR to HDR10</h3>
								<span className="badge badge-warning badge-outline badge-sm">Experimental</span>
							</div>
							<p className="mt-2 max-w-2xl text-base-content/65 text-sm leading-relaxed">
								Expand compatible SDR movies and episodes into a 10-bit HDR10 presentation using
								FFmpeg libplacebo. Native HDR content remains untouched.
							</p>
						</div>
					</div>
					<label className="flex cursor-pointer items-center gap-3 rounded-xl border border-base-300 bg-base-100/80 px-4 py-3 shadow-sm">
						<span className="font-semibold text-sm">{data.enabled ? "Enabled" : "Off"}</span>
						<input
							type="checkbox"
							className="toggle toggle-warning"
							checked={data.enabled === true}
							disabled={isReadOnly}
							onChange={(event) => updateData({ ...data, enabled: event.target.checked })}
						/>
					</label>
				</div>
			</div>

			<div>
				<div className="mb-3 flex items-center justify-between gap-3">
					<div>
						<h3 className="font-bold text-lg">Enhancement strength</h3>
						<p className="text-base-content/55 text-sm">
							Choose how noticeable the HDR expansion should be.
						</p>
					</div>
				</div>
				<div className="grid gap-3 md:grid-cols-3">
					{STRENGTHS.map((option) => {
						const selected = data.strength === option.id;
						const Icon = option.icon;
						return (
							<button
								key={option.id}
								type="button"
								disabled={isReadOnly || data.enabled !== true}
								onClick={() => updateData({ ...data, strength: option.id })}
								className={`rounded-2xl border p-4 text-left transition ${
									selected
										? "border-warning bg-warning/10 shadow-[0_0_0_1px_hsl(var(--wa)/0.25)]"
										: "border-base-300 bg-base-200/45 hover:border-warning/40"
								} disabled:cursor-not-allowed disabled:opacity-50`}
							>
								<div className="flex items-start justify-between gap-3">
									<Icon
										className={`h-5 w-5 ${selected ? "text-warning" : "text-base-content/55"}`}
									/>
									<span className="badge badge-ghost badge-sm">{option.badge}</span>
								</div>
								<div className="mt-3 font-bold">{option.label}</div>
								<p className="mt-1 text-base-content/60 text-xs leading-relaxed">
									{option.description}
								</p>
							</button>
						);
					})}
				</div>
			</div>

			<div className="rounded-2xl border border-base-300 bg-base-200/35 p-5">
				<div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
					<div className="flex gap-3">
						<Tv2 className="mt-0.5 h-5 w-5 shrink-0 text-primary" />
						<div>
							<h3 className="font-bold">Compatibility check</h3>
							<p className="mt-1 text-base-content/60 text-sm">
								Verify that this server can initialize the exact libplacebo HDR filter before
								enabling it.
							</p>
						</div>
					</div>
					<button
						type="button"
						className="btn btn-outline btn-sm shrink-0"
						disabled={isDetecting}
						onClick={runDetection}
					>
						<RefreshCw className={`h-4 w-4 ${isDetecting ? "animate-spin" : ""}`} />
						{isDetecting ? "Checking…" : "Auto Detect"}
					</button>
				</div>

				{compatibility && (
					<div
						className={`mt-4 flex gap-3 rounded-xl border p-3 ${
							compatibility.available
								? "border-success/30 bg-success/10"
								: "border-warning/30 bg-warning/10"
						}`}
					>
						{compatibility.available ? (
							<CheckCircle2 className="h-5 w-5 shrink-0 text-success" />
						) : (
							<AlertTriangle className="h-5 w-5 shrink-0 text-warning" />
						)}
						<div>
							<div className="font-semibold text-sm">{compatibility.status}</div>
							{compatibility.details && (
								<div className="mt-0.5 break-words text-base-content/65 text-xs">
									{compatibility.details}
								</div>
							)}
						</div>
					</div>
				)}
				{detectionError && (
					<div className="mt-4 flex gap-2 rounded-xl border border-error/30 bg-error/10 p-3 text-error text-sm">
						<AlertTriangle className="h-5 w-5 shrink-0" />
						<span>{detectionError}</span>
					</div>
				)}
			</div>

			<div className="flex gap-3 rounded-xl border border-info/25 bg-info/5 p-4 text-sm">
				<Info className="mt-0.5 h-5 w-5 shrink-0 text-info" />
				<p className="text-base-content/70 leading-relaxed">
					This applies only to SDR on-demand playback on HDR10-capable TVs. It requires a HEVC Main
					10 transcode, can run with your selected upscaler, and safely returns to ordinary SDR if
					the HDR filter or encoder is unavailable. Tube TV keeps its existing stable output mode.
				</p>
			</div>

			<div className="flex justify-end">
				<button
					type="button"
					className="btn btn-primary min-w-36"
					disabled={isReadOnly || isUpdating || !hasChanges}
					onClick={handleSave}
				>
					{isUpdating ? (
						<span className="loading loading-spinner loading-sm" />
					) : (
						<Save className="h-4 w-4" />
					)}
					Save Changes
				</button>
			</div>
		</div>
	);
}
