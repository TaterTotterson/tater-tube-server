import { Cpu, Info, RefreshCw, Save } from "lucide-react";
import { useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import type {
	ConfigResponse,
	SegmentCacheConfig,
	StreamingConfig,
	TranscodingHardwareDetection,
	TranscodingConfig,
} from "../../types/config";

interface StreamingConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (
		section: string,
		data: StreamingConfig | SegmentCacheConfig | TranscodingConfig,
	) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

export function StreamingConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: StreamingConfigSectionProps) {
	const [streamingData, setStreamingData] = useState<StreamingConfig>(config.streaming);
	const [transcodingData, setTranscodingData] = useState<TranscodingConfig>(config.transcoding);
	const [cacheData, setCacheData] = useState<SegmentCacheConfig>(config.segment_cache);
	const [hasChanges, setHasChanges] = useState(false);
	const [hardwareDetection, setHardwareDetection] =
		useState<TranscodingHardwareDetection | null>(null);
	const [isDetectingHardware, setIsDetectingHardware] = useState(false);
	const [hardwareDetectionError, setHardwareDetectionError] = useState("");

	// Sync form data when config changes from external sources (reload)
	useEffect(() => {
		setStreamingData(config.streaming);
		setTranscodingData(config.transcoding);
		setCacheData(config.segment_cache);
		setHasChanges(false);
	}, [config.streaming, config.transcoding, config.segment_cache]);

	const checkChanges = (
		newStreaming: StreamingConfig,
		newTranscoding: TranscodingConfig,
		newCache: SegmentCacheConfig,
	) => {
		const streamingChanged = JSON.stringify(newStreaming) !== JSON.stringify(config.streaming);
		const transcodingChanged = JSON.stringify(newTranscoding) !== JSON.stringify(config.transcoding);
		const cacheChanged = JSON.stringify(newCache) !== JSON.stringify(config.segment_cache);
		setHasChanges(streamingChanged || transcodingChanged || cacheChanged);
	};

	const handleStreamingChange = (field: keyof StreamingConfig, value: number) => {
		const newData = { ...streamingData, [field]: value };
		setStreamingData(newData);
		checkChanges(newData, transcodingData, cacheData);
	};

	const handleTranscodingChange = (
		field: keyof TranscodingConfig,
		value: boolean | string,
	) => {
		const newData = { ...transcodingData, [field]: value };
		setTranscodingData(newData);
		checkChanges(streamingData, newData, cacheData);
	};

	const handleCacheChange = (field: keyof SegmentCacheConfig, value: boolean | string | number) => {
		const newData = { ...cacheData, [field]: value };
		setCacheData(newData);
		checkChanges(streamingData, transcodingData, newData);
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
				checkChanges(streamingData, nextTranscoding, cacheData);
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

		const streamingChanged = JSON.stringify(streamingData) !== JSON.stringify(config.streaming);
		const transcodingChanged = JSON.stringify(transcodingData) !== JSON.stringify(config.transcoding);
		const cacheChanged = JSON.stringify(cacheData) !== JSON.stringify(config.segment_cache);

		if (streamingChanged) {
			await onUpdate("streaming", streamingData);
		}
		if (transcodingChanged) {
			await onUpdate("transcoding", transcodingData);
		}
		if (cacheChanged) {
			await onUpdate("segment_cache", cacheData);
		}
		setHasChanges(false);
	};

	return (
		<div className="space-y-10">
			{/* Playback Tuning */}
			<div>
				<h3 className="font-bold text-base-content text-lg">Playback Tuning</h3>
				<p className="text-base-content/50 text-sm">
					Optimize how Tater Tube Server sends media to your players.
				</p>
			</div>

			<div className="space-y-8">
				{/* Prefetch Slider */}
				<div className="space-y-6 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
						<div className="min-w-0">
							<h4 className="font-bold text-base-content text-sm">Segment Prefetch</h4>
							<p className="mt-1 break-words text-[11px] text-base-content/50 leading-relaxed">
								Number of Usenet articles to download ahead of current playback position.
							</p>
						</div>
						<div className="flex shrink-0 items-center gap-3">
							<span className="font-black font-mono text-primary text-xl">
								{streamingData.max_prefetch}
							</span>
							<span className="font-bold text-base-content/60 text-xs uppercase">segments</span>
						</div>
					</div>

					<div className="space-y-4">
						<input
							type="range"
							min="1"
							max="100"
							value={streamingData.max_prefetch}
							step="1"
							className="range range-primary range-sm w-full [&::-webkit-slider-runnable-track]:rounded-full"
							disabled={isReadOnly}
							onChange={(e) =>
								handleStreamingChange("max_prefetch", Number.parseInt(e.target.value, 10))
							}
						/>
						<div className="flex justify-between px-2 font-black text-base-content/50 text-xs">
							<span>1</span>
							<span>20</span>
							<span>40</span>
							<span>60</span>
							<span>80</span>
							<span>100</span>
						</div>
					</div>
				</div>

				{/* Guidance */}
				<div className="alert items-start rounded-2xl border border-info/20 bg-info/5 p-4 shadow-sm">
					<Info className="mt-0.5 h-5 w-5 shrink-0 text-info" />
					<div className="min-w-0 flex-1">
						<div className="font-bold text-info text-xs uppercase tracking-wider">
							Performance Note
						</div>
						<div className="mt-1 break-words text-[11px] leading-relaxed opacity-80">
							Higher values improve stability on slow connections but increase initial memory usage.
							Default (60) is recommended for most 4K streaming scenarios.
						</div>
					</div>
				</div>
			</div>

			{/* Transcoding */}
			<div className="border-base-200 border-t pt-10">
				<h3 className="font-bold text-base-content text-lg">FFmpeg Transcoding</h3>
				<p className="text-base-content/50 text-sm">
					Optionally convert streams to Tater Tube friendly H.264/AAC playback profiles.
				</p>
			</div>

			<div className="space-y-8">
				<div className="flex items-center justify-between rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="min-w-0">
						<h4 className="font-bold text-base-content text-sm">Enable Transcoding</h4>
						<p className="mt-1 break-words text-[11px] text-base-content/50 leading-relaxed">
							When enabled, direct stream URLs are piped through FFmpeg before playback.
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

				{transcodingData.enabled === true && (
					<>
						<div className="fade-in slide-in-from-top-2 animate-in grid gap-6 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6 md:grid-cols-2">
							<label className="form-control">
								<span className="label-text font-bold text-base-content text-sm">Playback Profile</span>
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
									onChange={(e) =>
										handleTranscodingChange("hardware_acceleration", e.target.value)
									}
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

						<div className="fade-in slide-in-from-top-2 animate-in space-y-4 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
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
											FFmpeg: {hardwareDetection.ffmpeg_available ? hardwareDetection.ffmpeg_path : "not found"}
											{hardwareDetection.recommended_device
												? ` · Device: ${hardwareDetection.recommended_device}`
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
													{option.device ? ` · ${option.device}` : ""}
												</div>
												{option.details && (
													<div className="mt-1 text-[11px] text-base-content/45">
														{option.details}
													</div>
												)}
											</div>
										))}
									</div>
								</div>
							)}
						</div>

						<div className="fade-in slide-in-from-top-2 animate-in grid gap-6 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6 md:grid-cols-2">
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
									Optional. VAAPI defaults to /dev/dri/renderD128 when left blank.
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
									Transcoded streams are sent as MPEG-TS with H.264 video and stereo AAC audio.
									Use direct play when your client can already decode the source smoothly.
								</div>
							</div>
						</div>
					</>
				)}
			</div>

			{/* Segment Cache */}
			<div className="border-base-200 border-t pt-10">
				<h3 className="font-bold text-base-content text-lg">Segment Cache</h3>
				<p className="text-base-content/50 text-sm">
					Cache decoded Usenet segments on disk so repeated reads avoid network round-trips.
				</p>
				<p className="mt-1 text-base-content/60 text-sm">
					The segment cache applies to direct stream playback and can help repeated reads avoid
					network round-trips.
				</p>
			</div>

			<div className="space-y-8">
				{/* Enabled toggle */}
				<div className="flex items-center justify-between rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="min-w-0">
						<h4 className="font-bold text-base-content text-sm">Enable Segment Cache</h4>
						<p className="mt-1 break-words text-[11px] text-base-content/50 leading-relaxed">
							When enabled, decoded segments are stored on disk and reused by the streaming engine.
						</p>
					</div>
					<input
						type="checkbox"
						className="toggle toggle-primary"
						checked={cacheData.enabled === true}
						disabled={isReadOnly}
						onChange={(e) => handleCacheChange("enabled", e.target.checked)}
					/>
				</div>

				{cacheData.enabled === true && (
					<>
						{/* Cache Path */}
						<div className="fade-in slide-in-from-top-2 animate-in space-y-3 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
							<div className="min-w-0">
								<h4 className="font-bold text-base-content text-sm">Cache Path</h4>
								<p className="mt-1 break-words text-[11px] text-base-content/50 leading-relaxed">
									Directory where cached segment data is stored. Use a fast disk (SSD/NVMe) for best
									results.
								</p>
							</div>
							<input
								type="text"
								className="input input-bordered w-full"
								value={cacheData.cache_path}
								disabled={isReadOnly}
								placeholder="/tmp/tater-tube-server-cache"
								onChange={(e) => handleCacheChange("cache_path", e.target.value)}
							/>
						</div>

						{/* Max Size slider */}
						<div className="fade-in slide-in-from-top-2 animate-in space-y-6 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
							<div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
								<div className="min-w-0">
									<h4 className="font-bold text-base-content text-sm">Maximum Cache Size</h4>
									<p className="mt-1 break-words text-[11px] text-base-content/50 leading-relaxed">
										Maximum disk space the segment cache may use before evicting old entries.
									</p>
								</div>
								<div className="flex shrink-0 items-center gap-3">
									<span className="font-black font-mono text-primary text-xl">
										{cacheData.max_size_gb}
									</span>
									<span className="font-bold text-base-content/60 text-xs uppercase">GB</span>
								</div>
							</div>

							<div className="space-y-4">
								<input
									type="range"
									min="1"
									max="1000"
									value={cacheData.max_size_gb}
									step="1"
									className="range range-primary range-sm w-full [&::-webkit-slider-runnable-track]:rounded-full"
									disabled={isReadOnly}
									onChange={(e) =>
										handleCacheChange("max_size_gb", Number.parseInt(e.target.value, 10))
									}
								/>
								<div className="flex justify-between px-2 font-black text-base-content/50 text-xs">
									<span>1</span>
									<span>250</span>
									<span>500</span>
									<span>750</span>
									<span>1000</span>
								</div>
							</div>
						</div>

						{/* Expiry slider */}
						<div className="fade-in slide-in-from-top-2 animate-in space-y-6 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
							<div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
								<div className="min-w-0">
									<h4 className="overflow-visible whitespace-normal font-bold text-base-content text-sm">
										Cache Expiry
									</h4>
									<p className="mt-1 break-words text-[11px] text-base-content/50 leading-relaxed">
										How long cached segments are kept before automatic eviction.
									</p>
								</div>
								<div className="mt-1 flex shrink-0 items-center justify-start gap-3 sm:mt-0 sm:justify-end">
									<span className="font-black font-mono text-primary text-xl">
										{cacheData.expiry_hours}
									</span>
									<span className="font-bold text-base-content/60 text-xs uppercase">hours</span>
								</div>
							</div>

							<div className="space-y-4">
								<input
									type="range"
									min="1"
									max="168"
									value={cacheData.expiry_hours}
									step="1"
									className="range range-primary range-sm w-full [&::-webkit-slider-runnable-track]:rounded-full"
									disabled={isReadOnly}
									onChange={(e) =>
										handleCacheChange("expiry_hours", Number.parseInt(e.target.value, 10))
									}
								/>
								<div className="flex justify-between px-2 font-black text-base-content/50 text-xs">
									<span>1h</span>
									<span>42h</span>
									<span>84h</span>
									<span>126h</span>
									<span>168h</span>
								</div>
							</div>
						</div>

						{/* Info box */}
						<div className="fade-in slide-in-from-top-2 alert animate-in items-start rounded-2xl border border-info/20 bg-info/5 p-4 shadow-sm">
							<Info className="mt-0.5 h-5 w-5 shrink-0 text-info" />
							<div className="min-w-0 flex-1">
								<div className="font-bold text-info text-xs uppercase tracking-wider">
									How It Works
								</div>
								<div className="mt-1 break-words text-[11px] leading-relaxed opacity-80">
									Each cached entry corresponds to one decoded Usenet article (~750 KB). On a cache
									hit the data is served directly from disk with no network round-trip. Eviction
									runs automatically every 5 minutes, removing expired entries and enforcing the
									size limit by evicting least-recently-used entries first.
								</div>
							</div>
						</div>
					</>
				)}
			</div>

			{/* Save Button */}
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
