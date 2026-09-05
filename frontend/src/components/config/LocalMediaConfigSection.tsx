import {
	AlertTriangle,
	CircleCheck,
	Disc3,
	ExternalLink,
	FileText,
	Film,
	Folder,
	FolderOpen,
	HardDrive,
	Image,
	ImageOff,
	KeyRound,
	Library,
	ListMusic,
	Lock,
	LockOpen,
	Music2,
	Plus,
	RefreshCw,
	Save,
	Search,
	Trash2,
	Tv,
	WandSparkles,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useToast } from "../../contexts/ToastContext";
import {
	useLocalMediaLibrary,
	useLocalMediaScanStatus,
	useRefreshLocalMediaAlbumArtwork,
	useRefreshLocalMediaVideoArtwork,
	useStartLocalMediaScan,
	useTestLocalMediaTMDBKey,
	useUpdateLocalMediaAlbumArtwork,
} from "../../hooks/useApi";
import type {
	ConfigResponse,
	LocalMediaCategory,
	LocalMediaConfig,
	LocalMediaLibraryStats,
	LocalMediaLibraryType,
	LocalMediaMusicAlbum,
} from "../../types/config";
import { LocalMediaScanProgress } from "../system/LocalMediaScanProgress";
import { BytesDisplay } from "../ui/BytesDisplay";
import { ConfigMiniTabs } from "./ConfigMiniTabs";
import { FolderPickerModal } from "./FolderPickerModal";

interface LocalMediaConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: LocalMediaConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

const DEFAULT_LOCAL_MEDIA: LocalMediaConfig = {
	enabled: false,
	audiodb_enabled: true,
	audiodb_api_key: "",
	audiodb_api_key_set: false,
	tmdb_enabled: true,
	tmdb_api_key: "",
	tmdb_api_key_set: false,
	categories: [],
};

const LIBRARY_TABS: Array<{
	id: LocalMediaLibraryType;
	label: string;
	description: string;
	defaultName: string;
}> = [
	{ id: "movies", label: "Movies", description: "Feature films and videos", defaultName: "Movies" },
	{
		id: "tv",
		label: "TV Shows",
		description: "Series, seasons, and episodes",
		defaultName: "TV Shows",
	},
	{
		id: "music",
		label: "Music",
		description: "Artists, albums, songs, and artwork",
		defaultName: "Music",
	},
	{
		id: "folders",
		label: "Folders",
		description: "Browse media using its folder structure",
		defaultName: "Folders",
	},
];

const EMPTY_STATS: LocalMediaLibraryStats = {
	files: 0,
	movies: 0,
	shows: 0,
	episodes: 0,
	artists: 0,
	albums: 0,
	songs: 0,
	artwork: 0,
	missing_artwork: 0,
	metadata: 0,
	missing_metadata: 0,
	errors: 0,
	size_bytes: 0,
};

const LIBRARY_PAGE_SIZE = 24;

function slug(value: string) {
	return value
		.toLowerCase()
		.trim()
		.replace(/[^a-z0-9]+/g, "-")
		.replace(/^-+|-+$/g, "")
		.slice(0, 64);
}

function libraryType(value?: string): LocalMediaLibraryType {
	return value === "tv" || value === "music" || value === "folders" ? value : "movies";
}

function normalize(config: ConfigResponse): LocalMediaConfig {
	const source = config.local_media ?? DEFAULT_LOCAL_MEDIA;
	return {
		enabled: source.enabled ?? false,
		audiodb_enabled: source.audiodb_enabled ?? true,
		audiodb_api_key: "",
		audiodb_api_key_set: source.audiodb_api_key_set ?? false,
		tmdb_enabled: source.tmdb_enabled ?? true,
		tmdb_api_key: "",
		tmdb_api_key_set: source.tmdb_api_key_set ?? false,
		categories: (source.categories ?? []).map((category) => ({
			id: category.id || slug(category.name || "local"),
			name: category.name || "Local",
			library_type: libraryType(category.library_type),
			paths: category.paths ?? [],
			enabled: category.enabled ?? true,
		})),
	};
}

function libraryIcon(type: LocalMediaLibraryType) {
	switch (type) {
		case "movies":
			return <Film className="h-4 w-4" />;
		case "tv":
			return <Tv className="h-4 w-4" />;
		case "music":
			return <Music2 className="h-4 w-4" />;
		default:
			return <Folder className="h-4 w-4" />;
	}
}

function musicMetadataComplete(album: LocalMediaMusicAlbum) {
	return (
		!album.metadata_available ||
		(album.has_metadata && (!album.artist_metadata_available || album.has_artist_metadata))
	);
}

function statCards(type: LocalMediaLibraryType, stats: LocalMediaLibraryStats) {
	const size = {
		label: "Storage",
		value: <BytesDisplay bytes={stats.size_bytes} />,
		icon: <HardDrive className="h-4 w-4" />,
	};
	if (type === "music") {
		return [
			{
				label: "Artists",
				value: stats.artists.toLocaleString(),
				icon: <Music2 className="h-4 w-4" />,
			},
			{
				label: "Albums",
				value: stats.albums.toLocaleString(),
				icon: <Disc3 className="h-4 w-4" />,
			},
			{
				label: "Songs",
				value: stats.songs.toLocaleString(),
				icon: <ListMusic className="h-4 w-4" />,
			},
			{
				label: "With Art",
				value: stats.artwork.toLocaleString(),
				icon: <Image className="h-4 w-4" />,
			},
			{
				label: "Missing Art",
				value: stats.missing_artwork.toLocaleString(),
				icon: <ImageOff className="h-4 w-4" />,
			},
			{
				label: "With NFO",
				value: stats.metadata.toLocaleString(),
				icon: <FileText className="h-4 w-4" />,
			},
			{
				label: "Missing NFO",
				value: stats.missing_metadata.toLocaleString(),
				icon: <FileText className="h-4 w-4" />,
			},
			size,
		];
	}
	if (type === "tv") {
		return [
			{ label: "Shows", value: stats.shows.toLocaleString(), icon: <Tv className="h-4 w-4" /> },
			{
				label: "Episodes",
				value: stats.episodes.toLocaleString(),
				icon: <Film className="h-4 w-4" />,
			},
			{
				label: "With Art",
				value: stats.artwork.toLocaleString(),
				icon: <Image className="h-4 w-4" />,
			},
			{
				label: "Missing Art",
				value: stats.missing_artwork.toLocaleString(),
				icon: <ImageOff className="h-4 w-4" />,
			},
			{
				label: "With NFO",
				value: stats.metadata.toLocaleString(),
				icon: <FileText className="h-4 w-4" />,
			},
			{
				label: "Missing NFO",
				value: stats.missing_metadata.toLocaleString(),
				icon: <FileText className="h-4 w-4" />,
			},
			size,
		];
	}
	if (type === "movies") {
		return [
			{ label: "Movies", value: stats.movies.toLocaleString(), icon: <Film className="h-4 w-4" /> },
			{
				label: "With Art",
				value: stats.artwork.toLocaleString(),
				icon: <Image className="h-4 w-4" />,
			},
			{
				label: "Missing Art",
				value: stats.missing_artwork.toLocaleString(),
				icon: <ImageOff className="h-4 w-4" />,
			},
			{
				label: "With NFO",
				value: stats.metadata.toLocaleString(),
				icon: <FileText className="h-4 w-4" />,
			},
			{
				label: "Missing NFO",
				value: stats.missing_metadata.toLocaleString(),
				icon: <FileText className="h-4 w-4" />,
			},
			size,
		];
	}
	return [
		{
			label: "Media Files",
			value: stats.files.toLocaleString(),
			icon: <Folder className="h-4 w-4" />,
		},
		size,
	];
}

export function LocalMediaConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: LocalMediaConfigSectionProps) {
	const { showToast } = useToast();
	const [formData, setFormData] = useState<LocalMediaConfig>(() => normalize(config));
	const [hasChanges, setHasChanges] = useState(false);
	const [activeTab, setActiveTab] = useState<LocalMediaLibraryType>("music");
	const [search, setSearch] = useState("");
	const [missingOnly, setMissingOnly] = useState(false);
	const [libraryOffset, setLibraryOffset] = useState(0);
	const [tmdbTestResult, setTmdbTestResult] = useState<{
		type: "success" | "error";
		message: string;
	} | null>(null);
	const scanWasRunning = useRef(false);
	const [folderPicker, setFolderPicker] = useState<{
		categoryIndex: number;
		pathIndex: number;
		initialPath: string;
	} | null>(null);

	const library = useLocalMediaLibrary({
		type: activeTab,
		q: activeTab !== "folders" ? search.trim() : undefined,
		missing_only: activeTab !== "folders" ? missingOnly : undefined,
		limit: LIBRARY_PAGE_SIZE,
		offset: activeTab !== "folders" ? libraryOffset : 0,
	});
	const scan = useLocalMediaScanStatus();
	const startScan = useStartLocalMediaScan();
	const refreshArtwork = useRefreshLocalMediaAlbumArtwork();
	const refreshVideoArtwork = useRefreshLocalMediaVideoArtwork();
	const testTMDBKey = useTestLocalMediaTMDBKey();
	const updateArtwork = useUpdateLocalMediaAlbumArtwork();

	useEffect(() => {
		setFormData(normalize(config));
		setHasChanges(false);
		setTmdbTestResult(null);
	}, [config]);

	useEffect(() => {
		const running = scan.data?.running ?? false;
		if (scanWasRunning.current && !running) {
			void library.refetch();
		}
		scanWasRunning.current = running;
	}, [library.refetch, scan.data?.running]);

	const update = (next: LocalMediaConfig) => {
		setFormData(next);
		setHasChanges(JSON.stringify(next) !== JSON.stringify(normalize(config)));
	};

	const updateCategory = (index: number, patch: Partial<LocalMediaCategory>) => {
		const categories = formData.categories.map((category, i) => {
			if (i !== index) return category;
			const next = { ...category, ...patch };
			if (patch.name !== undefined && (!next.id || next.id === slug(category.name))) {
				next.id = slug(patch.name) || next.id;
			}
			return next;
		});
		update({ ...formData, categories });
	};

	const addCategory = () => {
		const tab = LIBRARY_TABS.find((row) => row.id === activeTab) ?? LIBRARY_TABS[0];
		const matching = formData.categories.filter(
			(category) => libraryType(category.library_type) === activeTab,
		).length;
		const name = matching === 0 ? tab.defaultName : `${tab.defaultName} ${matching + 1}`;
		const usedIDs = new Set(formData.categories.map((category) => category.id));
		let id = slug(name) || activeTab;
		let suffix = 2;
		while (usedIDs.has(id)) {
			id = `${slug(name) || activeTab}-${suffix}`;
			suffix++;
		}
		update({
			...formData,
			categories: formData.categories.concat([
				{ id, name, library_type: activeTab, paths: [""], enabled: true },
			]),
		});
	};

	const removeCategory = (index: number) => {
		update({ ...formData, categories: formData.categories.filter((_, i) => i !== index) });
	};

	const addPath = (categoryIndex: number) => {
		const category = formData.categories[categoryIndex];
		const pathIndex = (category.paths ?? []).length;
		updateCategory(categoryIndex, { paths: (category.paths ?? []).concat([""]) });
		setFolderPicker({ categoryIndex, pathIndex, initialPath: "/" });
	};

	const updatePath = (categoryIndex: number, pathIndex: number, value: string) => {
		const category = formData.categories[categoryIndex];
		const paths = (category.paths ?? []).map((path, i) => (i === pathIndex ? value : path));
		updateCategory(categoryIndex, { paths });
	};

	const removePath = (categoryIndex: number, pathIndex: number) => {
		const category = formData.categories[categoryIndex];
		const paths = (category.paths ?? []).filter((_, i) => i !== pathIndex);
		updateCategory(categoryIndex, { paths: paths.length > 0 ? paths : [""] });
	};

	const handleSave = async () => {
		if (!onUpdate || !hasChanges) return;
		const next = {
			enabled: formData.enabled,
			audiodb_enabled: formData.audiodb_enabled ?? true,
			audiodb_api_key: formData.audiodb_api_key?.trim() ?? "",
			tmdb_enabled: formData.tmdb_enabled ?? true,
			tmdb_api_key: formData.tmdb_api_key?.trim() ?? "",
			categories: formData.categories.map((category) => ({
				...category,
				id: slug(category.id || category.name),
				name: category.name.trim(),
				library_type: libraryType(category.library_type),
				paths: (category.paths ?? []).map((path) => path.trim()).filter(Boolean),
				enabled: category.enabled ?? true,
			})),
		};
		try {
			await onUpdate("local_media", next);
			setFormData({
				...next,
				audiodb_api_key: "",
				audiodb_api_key_set:
					formData.audiodb_api_key_set || (formData.audiodb_api_key?.trim() ?? "") !== "",
				tmdb_api_key: "",
				tmdb_api_key_set: formData.tmdb_api_key_set || (formData.tmdb_api_key?.trim() ?? "") !== "",
			});
			setHasChanges(false);
			await startScan.mutateAsync({ scrapeMissingArtwork: false });
			showToast({ type: "success", title: "Local media saved", message: "Library scan started." });
		} catch (error) {
			showToast({
				type: "error",
				title: "Could not save local media",
				message: error instanceof Error ? error.message : "Unknown error",
			});
			throw error;
		}
	};

	const beginScan = async (withArtwork: boolean) => {
		try {
			await startScan.mutateAsync({
				scrapeMissingArtwork: withArtwork,
				artworkLibraryType: withArtwork ? activeTab : undefined,
			});
			showToast({
				type: "info",
				title: withArtwork ? "Media matching started" : "Library scan started",
				message: withArtwork
					? activeTab === "music"
						? "Albums will be matched carefully for missing artwork, genres, and NFO metadata in the background."
						: "Missing posters and NFO metadata will be matched carefully and saved beside the media."
					: "Tater Tube is updating the local media index.",
			});
		} catch (error) {
			showToast({
				type: "error",
				title: "Could not start scan",
				message: error instanceof Error ? error.message : "Unknown error",
			});
		}
	};

	const handleTMDBTest = async () => {
		try {
			const result = await testTMDBKey.mutateAsync(formData.tmdb_api_key?.trim() ?? "");
			setTmdbTestResult({
				type: "success",
				message:
					(formData.tmdb_api_key?.trim() ?? "")
						? "The key works — save your changes to start using it."
						: result.message,
			});
			showToast({
				type: "success",
				title: "TMDB is connected",
				message: "This server can privately request movie and TV metadata directly from TMDB.",
			});
		} catch (error) {
			const message = error instanceof Error ? error.message : "TMDB connection failed";
			setTmdbTestResult({ type: "error", message });
			showToast({ type: "error", title: "TMDB key did not work", message });
		}
	};

	const handleVideoArtworkRefresh = async (mediaId: string, hasArtwork: boolean) => {
		try {
			await refreshVideoArtwork.mutateAsync({ mediaId, force: hasArtwork });
			showToast({
				type: "success",
				title: hasArtwork ? "Artwork replaced" : "Media details found",
				message:
					"The poster and NFO metadata are saved beside the media for Tater Tube, Emby, and Jellyfin.",
			});
		} catch (error) {
			showToast({
				type: "warning",
				title: "No confident media match",
				message:
					error instanceof Error
						? error.message
						: "Try adding the release year to the folder name.",
			});
		}
	};

	const handleArtworkRefresh = async (albumId: string, hasArtwork: boolean) => {
		try {
			await refreshArtwork.mutateAsync({ albumId, force: hasArtwork });
			showToast({
				type: "success",
				title: hasArtwork ? "Album artwork replaced" : "Album details found",
				message: "Compatible album and artist NFO files are saved beside the music when possible.",
			});
		} catch (error) {
			showToast({
				type: "warning",
				title: "No confident artwork match",
				message: error instanceof Error ? error.message : "Try improving the album tags.",
			});
		}
	};

	const categoriesForTab = useMemo(
		() =>
			formData.categories
				.map((category, index) => ({ category, index }))
				.filter(({ category }) => libraryType(category.library_type) === activeTab),
		[activeTab, formData.categories],
	);
	const stats = library.data?.stats ?? EMPTY_STATS;
	const scanStatus = scan.data ?? library.data?.scan;
	const scanRunning = scanStatus?.running ?? false;
	const albumRows = library.data?.albums ?? [];
	const videoRows = library.data?.videos ?? [];
	const tmdbConfigured =
		formData.tmdb_enabled !== false &&
		(formData.tmdb_api_key_set || (formData.tmdb_api_key?.trim() ?? "") !== "");
	const tabInfo = LIBRARY_TABS.find((tab) => tab.id === activeTab) ?? LIBRARY_TABS[0];

	return (
		<div className="min-w-0 space-y-6">
			<section className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-5 sm:p-6">
				<div className="flex flex-col gap-5 lg:flex-row lg:items-start lg:justify-between">
					<div className="min-w-0">
						<div className="mb-2 flex items-center gap-2">
							<Library className="h-5 w-5 text-primary" />
							<h3 className="font-bold text-xl">Local Media Libraries</h3>
						</div>
						<p className="max-w-3xl text-base-content/60 text-sm leading-relaxed">
							Add folders that are visible to this server, scan them into a fast local index, and
							manage the media Tater Tube exposes to your players.
						</p>
					</div>
					<div className="flex flex-wrap items-center gap-3">
						<label className="flex items-center gap-3 rounded-xl border border-base-300 bg-base-100/75 px-4 py-2.5">
							<span className="font-bold text-sm">Enabled</span>
							<input
								type="checkbox"
								className="toggle toggle-primary toggle-sm"
								checked={formData.enabled}
								disabled={isReadOnly}
								onChange={(event) => update({ ...formData, enabled: event.target.checked })}
							/>
						</label>
						<button
							type="button"
							className="btn btn-outline"
							disabled={isReadOnly || hasChanges || scanRunning || startScan.isPending}
							onClick={() => beginScan(false)}
						>
							<RefreshCw className={`h-4 w-4 ${scanRunning ? "animate-spin" : ""}`} />
							Scan Libraries
						</button>
					</div>
				</div>

				{hasChanges && (
					<div className="alert alert-info mt-5 py-3 text-sm">
						<Save className="h-4 w-4" />
						<span>Save your folder changes before scanning.</span>
					</div>
				)}
				{scanStatus?.phase === "error" && (
					<div className="alert alert-error mt-5 py-3 text-sm">
						<AlertTriangle className="h-4 w-4" />
						<span>{scanStatus.error || scanStatus.message || "Library scan failed"}</span>
					</div>
				)}
				{scanRunning && scanStatus && (
					<div className="mt-5">
						<LocalMediaScanProgress status={scanStatus} />
					</div>
				)}
				{scanStatus?.phase === "complete" &&
					((scanStatus.albums_processed ?? 0) > 0 || (scanStatus.videos_processed ?? 0) > 0) && (
						<div className="alert alert-success mt-5 py-3 text-sm">
							<CircleCheck className="h-4 w-4" />
							<span>
								{(scanStatus.albums_processed ?? 0).toLocaleString()} albums checked ·{" "}
								{(scanStatus.videos_processed ?? 0).toLocaleString()} movies/shows checked ·{" "}
								{(scanStatus.artwork_found ?? 0).toLocaleString()} images found ·{" "}
								{(scanStatus.metadata_found ?? 0).toLocaleString()} NFO files created
							</span>
						</div>
					)}
			</section>

			<section className="space-y-5 rounded-2xl border border-base-300 bg-base-100/60 p-4 sm:p-5">
				<ConfigMiniTabs
					tabs={LIBRARY_TABS.map((tab) => ({
						id: tab.id,
						label: tab.label,
						icon: libraryIcon(tab.id),
						count: formData.categories.filter(
							(category) => libraryType(category.library_type) === tab.id,
						).length,
					}))}
					activeTab={activeTab}
					onChange={(nextTab) => {
						setActiveTab(nextTab);
						setSearch("");
						setMissingOnly(false);
						setLibraryOffset(0);
					}}
				/>

				<div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
					<div>
						<h4 className="font-bold text-lg">{tabInfo.label}</h4>
						<p className="text-base-content/55 text-sm">{tabInfo.description}</p>
					</div>
					<button
						type="button"
						className="btn btn-outline btn-sm"
						disabled={isReadOnly}
						onClick={addCategory}
					>
						<Plus className="h-4 w-4" />
						Add {tabInfo.label} Library
					</button>
				</div>

				<div className="grid grid-cols-2 gap-2 md:grid-cols-4 xl:grid-cols-8">
					{statCards(activeTab, stats).map((stat) => (
						<div key={stat.label} className="rounded-xl border border-base-300 bg-base-200/55 p-3">
							<div className="flex items-center gap-2 text-base-content/45 text-xs uppercase tracking-wide">
								{stat.icon}
								{stat.label}
							</div>
							<div className="mt-2 font-bold text-lg">{stat.value}</div>
						</div>
					))}
				</div>

				{activeTab === "music" && (
					<div className="rounded-xl border border-primary/20 bg-primary/5 p-4">
						<div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
							<div className="flex min-w-0 gap-3">
								<div className="mt-0.5 rounded-lg bg-primary/10 p-2 text-primary">
									<WandSparkles className="h-4 w-4" />
								</div>
								<div>
									<div className="font-bold text-sm">Music Metadata Enrichment</div>
									<p className="mt-1 max-w-2xl text-base-content/55 text-xs leading-relaxed">
										MusicBrainz identifies albums and artists first. TheAudioDB can fill missing
										genre, style, and cover details, then Tater Tube saves compatible album and
										artist NFO files beside structured music folders without replacing existing
										files.
									</p>
								</div>
							</div>
							<label className="flex shrink-0 items-center gap-3 rounded-lg border border-base-300 bg-base-100/75 px-3 py-2">
								<span className="font-bold text-xs">Use TheAudioDB</span>
								<input
									type="checkbox"
									className="toggle toggle-primary toggle-sm"
									checked={formData.audiodb_enabled ?? true}
									disabled={isReadOnly}
									onChange={(event) =>
										update({ ...formData, audiodb_enabled: event.target.checked })
									}
								/>
							</label>
						</div>
						{formData.audiodb_enabled !== false && (
							<label className="mt-4 block max-w-xl">
								<span className="font-bold text-base-content/65 text-xs">
									TheAudioDB API Key <span className="font-normal opacity-70">(optional)</span>
								</span>
								<input
									type="password"
									className="input input-bordered input-sm mt-1.5 w-full bg-base-100"
									placeholder={
										formData.audiodb_api_key_set
											? "Saved - leave blank to keep"
											: "Optional - public access is used automatically"
									}
									value={formData.audiodb_api_key ?? ""}
									disabled={isReadOnly}
									onChange={(event) => update({ ...formData, audiodb_api_key: event.target.value })}
								/>
								<span className="mt-1.5 block text-[11px] text-base-content/45">
									No account or key is required for the built-in public lookup.
								</span>
							</label>
						)}
					</div>
				)}

				{(activeTab === "movies" || activeTab === "tv") && (
					<div className="rounded-xl border border-primary/20 bg-primary/5 p-4">
						<div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
							<div className="flex min-w-0 gap-3">
								<div className="mt-0.5 rounded-lg bg-primary/10 p-2 text-primary">
									<WandSparkles className="h-4 w-4" />
								</div>
								<div>
									<div className="font-bold text-sm">Movie, TV & NFO Metadata</div>
									<p className="mt-1 max-w-2xl text-base-content/55 text-xs leading-relaxed">
										Existing Emby and Jellyfin posters and NFO files are always used first. When
										either is missing, TMDB can find a confident title and year match and save
										compatible sidecar files beside your media.
									</p>
								</div>
							</div>
							<label className="flex shrink-0 items-center gap-3 rounded-lg border border-base-300 bg-base-100/75 px-3 py-2">
								<span className="font-bold text-xs">Use TMDB</span>
								<input
									type="checkbox"
									className="toggle toggle-primary toggle-sm"
									checked={formData.tmdb_enabled ?? true}
									disabled={isReadOnly}
									onChange={(event) => update({ ...formData, tmdb_enabled: event.target.checked })}
								/>
							</label>
						</div>
						{formData.tmdb_enabled !== false && (
							<div className="mt-4 max-w-3xl rounded-xl border border-base-300 bg-base-100/55 p-3.5">
								<div className="flex flex-wrap items-center justify-between gap-2">
									<div className="flex items-center gap-2 font-bold text-base-content/70 text-xs">
										<KeyRound className="h-4 w-4 text-primary" />
										TMDB API Key or Read Access Token
									</div>
									<a
										href="https://www.themoviedb.org/settings/api"
										target="_blank"
										rel="noreferrer"
										className="inline-flex items-center gap-1.5 font-bold text-primary text-xs hover:underline"
									>
										Get a free TMDB key
										<ExternalLink className="h-3.5 w-3.5" />
									</a>
								</div>
								<div className="mt-2.5 grid min-w-0 gap-2 sm:grid-cols-[minmax(0,1fr)_auto]">
									<input
										type="password"
										className="input input-bordered input-sm min-w-0 bg-base-100"
										placeholder={
											formData.tmdb_api_key_set
												? "Key is saved - leave blank to keep it"
												: "Paste your API key or Read Access Token"
										}
										value={formData.tmdb_api_key ?? ""}
										disabled={isReadOnly}
										onChange={(event) => {
											setTmdbTestResult(null);
											update({ ...formData, tmdb_api_key: event.target.value });
										}}
									/>
									<button
										type="button"
										className="btn btn-outline btn-sm min-w-28"
										disabled={
											isReadOnly ||
											testTMDBKey.isPending ||
											(!formData.tmdb_api_key_set && !(formData.tmdb_api_key?.trim() ?? ""))
										}
										onClick={handleTMDBTest}
									>
										<RefreshCw
											className={`h-3.5 w-3.5 ${testTMDBKey.isPending ? "animate-spin" : ""}`}
										/>
										{testTMDBKey.isPending ? "Testing…" : "Test Key"}
									</button>
								</div>
								<p className="mt-2 text-[11px] text-base-content/50 leading-relaxed">
									Use your own TMDB key for better privacy. Your lookups aren’t tied to a shared
									app-wide key, so nobody else has access to that key’s activity—it stays between
									your server and TMDB. Existing local artwork and NFO files never require a key.
								</p>
								{tmdbTestResult && (
									<div
										className={`mt-2 flex items-center gap-2 text-xs ${
											tmdbTestResult.type === "success" ? "text-success" : "text-error"
										}`}
									>
										{tmdbTestResult.type === "success" ? (
											<CircleCheck className="h-4 w-4" />
										) : (
											<AlertTriangle className="h-4 w-4" />
										)}
										<span>{tmdbTestResult.message}</span>
									</div>
								)}
							</div>
						)}
						<div className="mt-4 flex max-w-2xl items-center gap-3 border-primary/10 border-t pt-3">
							<a href="https://www.themoviedb.org" target="_blank" rel="noreferrer">
								<img src="/tmdb-logo.svg" alt="TMDB" className="h-5 w-auto" />
							</a>
							<span className="text-[10px] text-base-content/45">
								This product uses the TMDB API but is not endorsed or certified by TMDB.
							</span>
						</div>
					</div>
				)}

				<div className="space-y-3">
					{categoriesForTab.length === 0 && (
						<div className="rounded-xl border border-base-300 border-dashed bg-base-200/30 px-5 py-8 text-center">
							{libraryIcon(activeTab)}
							<div className="mt-2 font-bold">No {tabInfo.label.toLowerCase()} library yet</div>
							<p className="mt-1 text-base-content/50 text-sm">
								Add one and choose a folder visible to this server.
							</p>
						</div>
					)}
					{categoriesForTab.map(({ category, index: categoryIndex }) => (
						<div
							key={category.id || `library-${categoryIndex}`}
							className="rounded-xl border border-base-300 bg-base-200/35 p-4"
						>
							<div className="flex flex-col gap-3 sm:flex-row sm:items-center">
								<div className="flex min-w-0 flex-1 items-center gap-3">
									<div className="rounded-lg bg-primary/10 p-2 text-primary">
										{libraryIcon(activeTab)}
									</div>
									<input
										type="text"
										className="input input-bordered min-w-0 flex-1 bg-base-100 font-bold"
										value={category.name}
										disabled={isReadOnly}
										onChange={(event) =>
											updateCategory(categoryIndex, { name: event.target.value })
										}
									/>
								</div>
								<label className="flex items-center gap-2 px-1 text-sm">
									Enabled
									<input
										type="checkbox"
										className="toggle toggle-primary toggle-sm"
										checked={category.enabled ?? true}
										disabled={isReadOnly}
										onChange={(event) =>
											updateCategory(categoryIndex, { enabled: event.target.checked })
										}
									/>
								</label>
								<button
									type="button"
									className="btn btn-ghost btn-sm text-error"
									disabled={isReadOnly}
									onClick={() => removeCategory(categoryIndex)}
								>
									<Trash2 className="h-4 w-4" />
									Remove
								</button>
							</div>

							<div className="mt-4 space-y-2">
								<div className="font-bold text-base-content/45 text-xs uppercase tracking-widest">
									Folders
								</div>
								{((category.paths ?? []).length > 0 ? category.paths : [""]).map(
									(path, pathIndex) => (
										<div
											key={`${category.id}-path-${pathIndex}`}
											className="flex flex-col gap-2 sm:flex-row"
										>
											<input
												type="text"
												className="input input-bordered w-full bg-base-100 font-mono text-sm"
												placeholder={`/media/${activeTab}`}
												value={path}
												disabled={isReadOnly}
												onChange={(event) =>
													updatePath(categoryIndex, pathIndex, event.target.value)
												}
											/>
											<button
												type="button"
												className="btn btn-outline shrink-0"
												disabled={isReadOnly}
												onClick={() =>
													setFolderPicker({ categoryIndex, pathIndex, initialPath: path || "/" })
												}
											>
												<FolderOpen className="h-4 w-4" />
												Browse
											</button>
											<button
												type="button"
												className="btn btn-ghost btn-square shrink-0"
												disabled={isReadOnly}
												onClick={() => removePath(categoryIndex, pathIndex)}
												aria-label="Remove folder"
											>
												<Trash2 className="h-4 w-4" />
											</button>
										</div>
									),
								)}
								<button
									type="button"
									className="btn btn-ghost btn-sm"
									disabled={isReadOnly}
									onClick={() => addPath(categoryIndex)}
								>
									<Plus className="h-4 w-4" /> Add Folder
								</button>
							</div>

							<details className="mt-3 text-xs">
								<summary className="cursor-pointer text-base-content/45 hover:text-base-content">
									Advanced library details
								</summary>
								<label className="mt-3 block max-w-md">
									<span className="font-bold text-base-content/50">Stable library ID</span>
									<input
										type="text"
										className="input input-bordered input-sm mt-1 w-full bg-base-100 font-mono"
										value={category.id}
										disabled={isReadOnly}
										onChange={(event) => updateCategory(categoryIndex, { id: event.target.value })}
									/>
								</label>
							</details>
						</div>
					))}
				</div>
			</section>

			{(activeTab === "movies" || activeTab === "tv") && (
				<section className="space-y-4 rounded-2xl border border-base-300 bg-base-100/70 p-4 sm:p-5">
					<div className="space-y-4">
						<div>
							<div className="flex items-center gap-2">
								{activeTab === "movies" ? (
									<Film className="h-5 w-5 text-primary" />
								) : (
									<Tv className="h-5 w-5 text-primary" />
								)}
								<h4 className="font-bold text-lg">
									Your {activeTab === "movies" ? "Movie" : "TV Show"} Library
								</h4>
							</div>
							<p className="mt-1 text-base-content/55 text-sm">
								{activeTab === "tv"
									? "Browse show coverage and add compatible backdrops, season posters, episode stills, and NFO sidecars."
									: "Browse poster and NFO coverage and fill only the sidecars that are still missing."}
							</p>
						</div>
						<div className="grid w-full min-w-0 gap-3 md:grid-cols-2 xl:grid-cols-[minmax(16rem,1fr)_minmax(11rem,auto)_minmax(13rem,auto)]">
							<label className="input input-bordered flex min-w-0 items-center gap-2 bg-base-100 md:col-span-2 xl:col-span-1">
								<Search className="h-4 w-4 text-base-content/40" />
								<input
									type="search"
									className="min-w-0 grow"
									placeholder={`Search ${activeTab === "movies" ? "movies" : "TV shows"}`}
									value={search}
									onChange={(event) => {
										setSearch(event.target.value);
										setLibraryOffset(0);
									}}
								/>
							</label>
							<fieldset
								className="grid min-w-0 grid-cols-2 gap-2"
								aria-label="Artwork coverage filter"
							>
								<button
									type="button"
									className={`btn ${!missingOnly ? "btn-primary" : "btn-outline"}`}
									onClick={() => {
										setMissingOnly(false);
										setLibraryOffset(0);
									}}
								>
									All
								</button>
								<button
									type="button"
									className={`btn ${missingOnly ? "btn-primary" : "btn-outline"}`}
									onClick={() => {
										setMissingOnly(true);
										setLibraryOffset(0);
									}}
								>
									<ImageOff className="h-4 w-4" />
									Missing
								</button>
							</fieldset>
							<button
								type="button"
								className="btn btn-secondary h-auto min-h-10 w-full min-w-0 whitespace-normal py-2 leading-tight"
								disabled={
									hasChanges ||
									scanRunning ||
									!tmdbConfigured ||
									(activeTab === "tv"
										? stats.shows === 0
										: stats.missing_artwork === 0 && stats.missing_metadata === 0)
								}
								onClick={() => beginScan(true)}
							>
								<WandSparkles className="h-4 w-4" />
								Find Missing
							</button>
						</div>
					</div>

					{library.isLoading && (
						<div className="flex h-52 items-center justify-center">
							<span className="loading loading-spinner loading-lg text-primary" />
						</div>
					)}
					{library.error && (
						<div className="alert alert-error">
							<AlertTriangle className="h-5 w-5" />
							<span>
								{library.error instanceof Error
									? library.error.message
									: "Unable to load the video library"}
							</span>
						</div>
					)}
					{!library.isLoading && !library.error && videoRows.length === 0 && (
						<div className="rounded-xl border border-base-300 border-dashed py-14 text-center">
							<ImageOff className="mx-auto h-10 w-10 text-base-content/25" />
							<div className="mt-3 font-bold">
								{library.data?.stale ? "Scan your local library" : "No titles match this view"}
							</div>
							<p className="mt-1 text-base-content/50 text-sm">
								{library.data?.stale
									? "Save your folders, then scan to build the artwork and metadata index."
									: "Try a different search or turn off the missing art/meta filter."}
							</p>
						</div>
					)}
					<div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-4 2xl:grid-cols-6">
						{videoRows.map((video) => (
							<article
								key={video.id}
								className="min-w-0 overflow-hidden rounded-xl border border-base-300 bg-base-200/45 [contain-intrinsic-size:24rem] [content-visibility:auto]"
							>
								<div className="relative aspect-[2/3] overflow-hidden bg-base-300/60">
									{video.artwork_url ? (
										<img
											src={video.artwork_url}
											alt={`${video.title} poster`}
											className="h-full w-full object-cover"
											loading="lazy"
											decoding="async"
											draggable={false}
										/>
									) : (
										<div className="flex h-full flex-col items-center justify-center gap-2 text-base-content/25">
											{video.media_type === "show" ? (
												<Tv className="h-14 w-14" />
											) : (
												<Film className="h-14 w-14" />
											)}
											<span className="text-xs">No artwork</span>
										</div>
									)}
									{video.has_artwork && (
										<span className="badge badge-sm absolute top-2 left-2 border-0 bg-black/70 text-white capitalize">
											{video.artwork_source === "local" ? "Library file" : video.artwork_source}
										</span>
									)}
									{video.has_metadata && (
										<span className="badge badge-sm absolute top-2 right-2 border-0 bg-black/70 text-white">
											<FileText className="h-3 w-3" />
											NFO
										</span>
									)}
								</div>
								<div className="space-y-3 p-3">
									<div className="min-w-0">
										<div className="truncate font-bold text-sm" title={video.title}>
											{video.title}
										</div>
										<div className="truncate text-base-content/55 text-xs">
											{video.media_type === "show" ? "TV Show" : "Movie"}
											{video.year ? ` · ${video.year}` : ""}
										</div>
									</div>
									<button
										type="button"
										className="btn btn-outline btn-xs w-full"
										disabled={refreshVideoArtwork.isPending || !tmdbConfigured || hasChanges}
										onClick={() => handleVideoArtworkRefresh(video.id, video.has_artwork)}
									>
										<WandSparkles className="h-3 w-3" />
										{video.has_artwork && video.has_metadata ? "Replace Art" : "Find Art & Meta"}
									</button>
									<div
										className="truncate font-mono text-[10px] text-base-content/35"
										title={video.path}
									>
										{video.category_name} / {video.path || "."}
									</div>
								</div>
							</article>
						))}
					</div>

					{(library.data?.total_videos ?? 0) > LIBRARY_PAGE_SIZE && (
						<div className="flex items-center justify-between border-base-300 border-t pt-4">
							<div className="text-base-content/50 text-xs">
								Titles {libraryOffset + 1}–
								{Math.min(libraryOffset + LIBRARY_PAGE_SIZE, library.data?.total_videos ?? 0)} of{" "}
								{(library.data?.total_videos ?? 0).toLocaleString()}
							</div>
							<div className="join">
								<button
									type="button"
									className="btn join-item btn-sm"
									disabled={libraryOffset === 0}
									onClick={() => setLibraryOffset(Math.max(0, libraryOffset - LIBRARY_PAGE_SIZE))}
								>
									Previous
								</button>
								<button
									type="button"
									className="btn join-item btn-sm"
									disabled={libraryOffset + LIBRARY_PAGE_SIZE >= (library.data?.total_videos ?? 0)}
									onClick={() => setLibraryOffset(libraryOffset + LIBRARY_PAGE_SIZE)}
								>
									Next
								</button>
							</div>
						</div>
					)}
				</section>
			)}

			{activeTab === "music" && (
				<section className="space-y-4 rounded-2xl border border-base-300 bg-base-100/70 p-4 sm:p-5">
					<div className="space-y-4">
						<div>
							<div className="flex items-center gap-2">
								<Disc3 className="h-5 w-5 text-primary" />
								<h4 className="font-bold text-lg">Your Music Library</h4>
							</div>
							<p className="mt-1 text-base-content/55 text-sm">
								Browse album artwork and NFO coverage and save compatible sidecars beside your
								music.
							</p>
						</div>
						<div className="grid w-full min-w-0 gap-3 md:grid-cols-2 xl:grid-cols-[minmax(16rem,1fr)_minmax(11rem,auto)_minmax(13rem,auto)]">
							<label className="input input-bordered flex min-w-0 items-center gap-2 bg-base-100 md:col-span-2 xl:col-span-1">
								<Search className="h-4 w-4 text-base-content/40" />
								<input
									type="search"
									className="min-w-0 grow"
									placeholder="Search albums or artists"
									value={search}
									onChange={(event) => {
										setSearch(event.target.value);
										setLibraryOffset(0);
									}}
								/>
							</label>
							<fieldset
								className="grid min-w-0 grid-cols-2 gap-2"
								aria-label="Artwork coverage filter"
							>
								<button
									type="button"
									className={`btn ${!missingOnly ? "btn-primary" : "btn-outline"}`}
									onClick={() => {
										setMissingOnly(false);
										setLibraryOffset(0);
									}}
								>
									All
								</button>
								<button
									type="button"
									className={`btn ${missingOnly ? "btn-primary" : "btn-outline"}`}
									onClick={() => {
										setMissingOnly(true);
										setLibraryOffset(0);
									}}
								>
									<ImageOff className="h-4 w-4" />
									Missing
								</button>
							</fieldset>
							<button
								type="button"
								className="btn btn-secondary h-auto min-h-10 w-full min-w-0 whitespace-normal py-2 leading-tight"
								disabled={
									hasChanges ||
									scanRunning ||
									stats.albums === 0 ||
									(stats.missing_artwork === 0 && stats.missing_metadata === 0)
								}
								onClick={() => beginScan(true)}
							>
								<WandSparkles className="h-4 w-4" />
								Find Missing
							</button>
						</div>
					</div>

					{library.isLoading && (
						<div className="flex h-52 items-center justify-center">
							<span className="loading loading-spinner loading-lg text-primary" />
						</div>
					)}
					{library.error && (
						<div className="alert alert-error">
							<AlertTriangle className="h-5 w-5" />
							<span>
								{library.error instanceof Error
									? library.error.message
									: "Unable to load music library"}
							</span>
						</div>
					)}
					{!library.isLoading && !library.error && albumRows.length === 0 && (
						<div className="rounded-xl border border-base-300 border-dashed py-14 text-center">
							<Disc3 className="mx-auto h-10 w-10 text-base-content/25" />
							<div className="mt-3 font-bold">
								{library.data?.stale ? "Scan your music library" : "No albums match this view"}
							</div>
							<p className="mt-1 text-base-content/50 text-sm">
								{library.data?.stale
									? "Save your folders, then run a library scan to build the album index."
									: "Try a different search or turn off the missing art/meta filter."}
							</p>
						</div>
					)}
					<div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-4 2xl:grid-cols-6">
						{albumRows.map((album) => (
							<article
								key={album.id}
								className="min-w-0 overflow-hidden rounded-xl border border-base-300 bg-base-200/45 [contain-intrinsic-size:20rem] [content-visibility:auto]"
							>
								<div className="relative aspect-square overflow-hidden bg-base-300/60">
									{album.artwork_url ? (
										<img
											src={album.artwork_url}
											alt={`${album.title} cover`}
											className="h-full w-full object-cover"
											loading="lazy"
											decoding="async"
											draggable={false}
										/>
									) : (
										<div className="flex h-full flex-col items-center justify-center gap-2 text-base-content/25">
											<Disc3 className="h-14 w-14" />
											<span className="text-xs">No artwork</span>
										</div>
									)}
									<div className="absolute top-2 left-2 flex gap-1">
										{album.has_artwork && (
											<span className="badge badge-sm border-0 bg-black/70 text-white capitalize">
												{album.artwork_source}
											</span>
										)}
										{album.artwork_locked && (
											<span className="badge badge-sm badge-warning">
												<Lock className="h-3 w-3" /> Locked
											</span>
										)}
									</div>
									<div className="absolute top-2 right-2 flex flex-col items-end gap-1">
										{album.has_metadata && (
											<span className="badge badge-sm border-0 bg-black/70 text-white">
												<FileText className="h-3 w-3" /> Album NFO
											</span>
										)}
										{album.has_artist_metadata && (
											<span className="badge badge-sm border-0 bg-black/70 text-white">
												<FileText className="h-3 w-3" /> Artist NFO
											</span>
										)}
									</div>
								</div>
								<div className="space-y-3 p-3">
									<div className="min-w-0">
										<div className="truncate font-bold text-sm" title={album.title}>
											{album.title}
										</div>
										<div className="truncate text-base-content/55 text-xs" title={album.artist}>
											{album.artist || "Unknown artist"}
										</div>
									</div>
									<div className="flex items-center justify-between text-[11px] text-base-content/45">
										<span>
											{album.track_count} {album.track_count === 1 ? "song" : "songs"}
											{album.year ? ` · ${album.year}` : ""}
										</span>
										<BytesDisplay bytes={album.size_bytes} mode="badge" />
									</div>
									<div className="grid grid-cols-2 gap-2">
										<button
											type="button"
											className="btn btn-outline btn-xs"
											disabled={
												refreshArtwork.isPending ||
												(album.artwork_locked && musicMetadataComplete(album))
											}
											onClick={() => handleArtworkRefresh(album.id, album.has_artwork)}
										>
											<WandSparkles className="h-3 w-3" />
											{album.has_artwork && musicMetadataComplete(album)
												? "Replace Art"
												: "Find Art & Meta"}
										</button>
										<button
											type="button"
											className="btn btn-ghost btn-xs"
											disabled={!album.has_artwork || updateArtwork.isPending}
											onClick={() =>
												updateArtwork.mutate({ albumId: album.id, locked: !album.artwork_locked })
											}
										>
											{album.artwork_locked ? (
												<LockOpen className="h-3 w-3" />
											) : (
												<Lock className="h-3 w-3" />
											)}
											{album.artwork_locked ? "Unlock" : "Lock"}
										</button>
									</div>
									<div
										className="truncate font-mono text-[10px] text-base-content/35"
										title={album.path}
									>
										{album.category_name} / {album.path || "."}
									</div>
								</div>
							</article>
						))}
					</div>

					{(library.data?.total_albums ?? 0) > LIBRARY_PAGE_SIZE && (
						<div className="flex items-center justify-between border-base-300 border-t pt-4">
							<div className="text-base-content/50 text-xs">
								Albums {libraryOffset + 1}–
								{Math.min(libraryOffset + LIBRARY_PAGE_SIZE, library.data?.total_albums ?? 0)} of{" "}
								{(library.data?.total_albums ?? 0).toLocaleString()}
							</div>
							<div className="join">
								<button
									type="button"
									className="btn join-item btn-sm"
									disabled={libraryOffset === 0}
									onClick={() => setLibraryOffset(Math.max(0, libraryOffset - LIBRARY_PAGE_SIZE))}
								>
									Previous
								</button>
								<button
									type="button"
									className="btn join-item btn-sm"
									disabled={libraryOffset + LIBRARY_PAGE_SIZE >= (library.data?.total_albums ?? 0)}
									onClick={() => setLibraryOffset(libraryOffset + LIBRARY_PAGE_SIZE)}
								>
									Next
								</button>
							</div>
						</div>
					)}
				</section>
			)}

			{!isReadOnly && (
				<div className="sticky bottom-3 z-20 flex justify-end rounded-2xl border border-base-300 bg-base-100/90 p-3 shadow-xl backdrop-blur">
					<button
						type="button"
						className="btn btn-primary rounded-full px-8"
						onClick={handleSave}
						disabled={!hasChanges || isUpdating || startScan.isPending}
					>
						{isUpdating ? (
							<span className="loading loading-spinner loading-sm" />
						) : (
							<Save className="h-4 w-4" />
						)}
						Save Local Media
					</button>
				</div>
			)}

			<FolderPickerModal
				open={folderPicker !== null}
				initialPath={folderPicker?.initialPath}
				onClose={() => setFolderPicker(null)}
				onSelect={(path) => {
					if (folderPicker) updatePath(folderPicker.categoryIndex, folderPicker.pathIndex, path);
					setFolderPicker(null);
				}}
			/>
		</div>
	);
}
