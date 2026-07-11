import { Cpu, Info, RefreshCw, Save } from "lucide-react";
import { useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import type {
	ConfigResponse,
	TranscodingConfig,
	TranscodingHardwareDetection,
} from "../../types/config";

interface TranscodingConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: TranscodingConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

export function TranscodingConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: TranscodingConfigSectionProps) {
	const [transcodingData, setTranscodingData] = useState<TranscodingConfig>(config.transcoding);
	const [hasChanges, setHasChanges] = useState(false);
	const [hardwareDetection, setHardwareDetection] = useState<TranscodingHardwareDetection | null>(
		null,
	);
	const [isDetectingHardware, setIsDetectingHardware] = useState(false);
	const [hardwareDetectionError, setHardwareDetectionError] = useState("");

	useEffect(() => {
		setTranscodingData(config.transcoding);
		setHasChanges(false);
	}, [config.transcoding]);

	const checkChanges = (newTranscoding: TranscodingConfig) => {
		setHasChanges(JSON.stringify(newTranscoding) !== JSON.stringify(config.transcoding));
	};

	const handleTranscodingChange = (field: keyof TranscodingConfig, value: boolean | string) => {
		const newData = { ...transcodingData, [field]: value };
		setTranscodingData(newData);
		checkChanges(newData);
	};

	const hardwareLabel = (id?: string) => {
		switch (id) {
			case "auto":
				return "Auto";
			case "vaapi":
				return "VAAPI";
			case "qsv":
				return "Intel QSV";
			case "nvenc":
				return "NVIDIA NVENC";
			case "videotoolbox":
				return "Apple VideoToolbox";
			case "v4l2m2m":
				return "Linux V4L2 M2M";
			default:
				return "Software x264";
		}
	};

	const runHardwareDetection = async () => {
		setIsDetectingHardware(true);
		setHardwareDetectionError("");
		try {
			const detection = await apiClient.detectTranscodingHardware();
			setHardwareDetection(detection);
			if (detection.recommended) {
				const nextTranscoding = {
					...transcodingData,
					hardware_acceleration: detection.recommended,
					hardware_device: detection.recommended_device || "",
				};
				setTranscodingData(nextTranscoding);
				checkChanges(nextTranscoding);
			}
		} catch (error) {
			setHardwareDetectionError(
				error instanceof Error ? error.message : "Hardware detection failed",
			);
		} finally {
			setIsDetectingHardware(false);
		}
	};

	const handleSave = async () => {
		if (!onUpdate || !hasChanges) return;
		await onUpdate("transcoding", transcodingData);
		setHasChanges(false);
	};

	return (
		<div className="space-y-10">
			<div>
				<h3 className="font-bold text-base-content text-lg">Hardware Transcoding</h3>
				<p className="text-base-content/50 text-sm">
					Convert Stream and Local media playback with FFmpeg profiles and hardware acceleration.
				</p>
			</div>

			<div className="space-y-8">
				<div className="flex items-center justify-between rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="min-w-0">
						<h4 className="font-bold text-base-content text-sm">Enable Transcoding</h4>
						<p className="mt-1 break-words text-[11px] text-base-content/50 leading-relaxed">
							When enabled, playable media URLs are piped through FFmpeg before playback unless
							direct play is requested.
						</p>
					</div>
					<input
						type="checkbox"
						className="toggle toggle-primary"
						checked={transcodingData.enabled === true}
						disabled={isReadOnly}
						onChange={(e) => handleTranscodingChange("enabled", e.target.checked)}
					/>
				</div>

				<div className="space-y-4 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
						<div className="min-w-0">
							<div className="mb-2 flex items-center gap-2">
								<Cpu className="h-4 w-4 text-base-content/60" />
								<h4 className="font-bold text-base-content text-sm">Hardware Detection</h4>
							</div>
							<p className="break-words text-[11px] text-base-content/50 leading-relaxed">
								Detects FFmpeg encoders and devices available to this server or container.
							</p>
						</div>
						<button
							type="button"
							className="btn btn-outline rounded-full"
							disabled={isReadOnly || isDetectingHardware}
							onClick={runHardwareDetection}
						>
							{isDetectingHardware ? (
								<span className="loading loading-spinner loading-sm" />
							) : (
								<RefreshCw className="h-4 w-4" />
							)}
							Auto Detect
						</button>
					</div>

					{hardwareDetectionError && (
						<div className="rounded-xl border border-error/30 bg-error/10 p-3 text-error text-xs">
							{hardwareDetectionError}
						</div>
					)}

					{hardwareDetection && (
						<div className="space-y-4">
							<div className="rounded-xl border border-primary/20 bg-primary/10 p-4">
								<div className="font-bold text-primary text-xs uppercase tracking-widest">
									Recommended
								</div>
								<div className="mt-1 font-black text-base-content text-lg">
									{hardwareLabel(hardwareDetection.recommended)}
								</div>
								<div className="mt-1 text-[11px] text-base-content/60">
									FFmpeg:{" "}
									{hardwareDetection.ffmpeg_available ? hardwareDetection.ffmpeg_path : "not found"}
									{hardwareDetection.recommended_device
										? ` - Device: ${hardwareDetection.recommended_device}`
										: ""}
								</div>
							</div>

							<div className="grid gap-2 md:grid-cols-2">
								{hardwareDetection.options.map((option) => (
									<div
										key={option.id}
										className={`rounded-xl border p-3 ${
											option.available
												? "border-success/30 bg-success/10"
												: "border-base-300 bg-base-100/60"
										}`}
									>
										<div className="flex items-center justify-between gap-3">
											<div className="font-bold text-sm">{option.label}</div>
											<div
												className={`badge badge-sm ${
													option.available ? "badge-success" : "badge-ghost"
												}`}
											>
												{option.available ? "Ready" : "No"}
											</div>
										</div>
										<div className="mt-1 text-[11px] text-base-content/60">
											{option.status}
											{option.device ? ` - ${option.device}` : ""}
										</div>
										{option.details && (
											<div className="mt-1 text-[11px] text-base-content/45">{option.details}</div>
										)}
									</div>
								))}
							</div>
						</div>
					)}
				</div>

				{transcodingData.enabled === true && (
					<>
						<div className="fade-in slide-in-from-top-2 grid animate-in gap-6 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6 md:grid-cols-2">
							<label className="form-control">
								<span className="label-text font-bold text-base-content text-sm">
									Playback Profile
								</span>
								<select
									className="select select-bordered mt-2 w-full"
									value={transcodingData.profile || "crt_480p"}
									disabled={isReadOnly}
									onChange={(e) => handleTranscodingChange("profile", e.target.value)}
								>
									<option value="crt_480p">CRT 480p</option>
									<option value="hdmi_1080p">HDMI 1080p</option>
									<option value="hdmi_4k">HDMI 4K</option>
								</select>
								<span className="mt-2 text-[11px] text-base-content/50">
									Controls output resolution and bitrate.
								</span>
							</label>

							<label className="form-control">
								<span className="label-text font-bold text-base-content text-sm">
									Hardware Acceleration
								</span>
								<select
									className="select select-bordered mt-2 w-full"
									value={transcodingData.hardware_acceleration || "none"}
									disabled={isReadOnly}
									onChange={(e) => handleTranscodingChange("hardware_acceleration", e.target.value)}
								>
									<option value="none">Software x264</option>
									<option value="auto">Auto</option>
									<option value="vaapi">VAAPI</option>
									<option value="qsv">Intel QSV</option>
									<option value="nvenc">NVIDIA NVENC</option>
									<option value="videotoolbox">Apple VideoToolbox</option>
									<option value="v4l2m2m">Linux V4L2 M2M</option>
								</select>
								<span className="mt-2 text-[11px] text-base-content/50">
									Requires matching FFmpeg encoder support and device access.
								</span>
							</label>
						</div>

						<div className="fade-in slide-in-from-top-2 grid animate-in gap-6 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6 md:grid-cols-2">
							<label className="form-control">
								<span className="label-text font-bold text-base-content text-sm">FFmpeg Path</span>
								<input
									type="text"
									className="input input-bordered mt-2 w-full"
									value={transcodingData.ffmpeg_path || "ffmpeg"}
									disabled={isReadOnly}
									placeholder="ffmpeg"
									onChange={(e) => handleTranscodingChange("ffmpeg_path", e.target.value)}
								/>
							</label>

							<label className="form-control">
								<span className="label-text font-bold text-base-content text-sm">
									Hardware Device
								</span>
								<input
									type="text"
									className="input input-bordered mt-2 w-full"
									value={transcodingData.hardware_device || ""}
									disabled={isReadOnly}
									placeholder="/dev/dri/renderD128"
									onChange={(e) => handleTranscodingChange("hardware_device", e.target.value)}
								/>
								<span className="mt-2 text-[11px] text-base-content/50">
									Optional. Used mainly by VAAPI. Intel QSV uses FFmpeg runtime device
									selection.
								</span>
							</label>
						</div>

						<div className="fade-in slide-in-from-top-2 alert animate-in items-start rounded-2xl border border-info/20 bg-info/5 p-4 shadow-sm">
							<Info className="mt-0.5 h-5 w-5 shrink-0 text-info" />
							<div className="min-w-0 flex-1">
								<div className="font-bold text-info text-xs uppercase tracking-wider">
									Output Format
								</div>
								<div className="mt-1 break-words text-[11px] leading-relaxed opacity-80">
									Transcoded streams are sent as MPEG-TS with H.264 video and stereo AAC audio. Use
									direct play when your client can already decode the source smoothly.
								</div>
							</div>
						</div>
					</>
				)}
			</div>

			{!isReadOnly && (
				<div className="flex justify-end border-base-200 border-t pt-4">
					<button
						type="button"
						className={`btn btn-primary px-10 shadow-lg shadow-primary/20 ${!hasChanges && "btn-ghost border-base-300"}`}
						onClick={handleSave}
						disabled={!hasChanges || isUpdating}
					>
						{isUpdating ? (
							<span className="loading loading-spinner loading-sm" />
						) : (
							<Save className="h-4 w-4" />
						)}
						{isUpdating ? "Saving..." : "Save Changes"}
					</button>
				</div>
			)}
		</div>
	);
}
