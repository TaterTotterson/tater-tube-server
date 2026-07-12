import { AlertCircle, LockKeyhole, RotateCcw, ShieldCheck } from "lucide-react";
import type React from "react";
import { useEffect, useMemo, useState } from "react";
import { useAuth } from "../../hooks/useAuth";

export function LoginPage() {
	const {
		isAuthenticated,
		checkRegistrationStatus,
		login,
		register,
		isLoading,
		error,
		clearError,
	} = useAuth();
	const [userCount, setUserCount] = useState<number>(1);
	const [passwordConfigured, setPasswordConfigured] = useState(true);
	const [statusLoading, setStatusLoading] = useState(true);
	const [hasConnectionError, setHasConnectionError] = useState(false);
	const [password, setPassword] = useState("");
	const [confirmPassword, setConfirmPassword] = useState("");
	const [localError, setLocalError] = useState<string | null>(null);

	useEffect(() => {
		const checkStatus = async () => {
			try {
				const status = await checkRegistrationStatus();
				setUserCount(status.user_count);
				setPasswordConfigured(status.password_configured);
				setHasConnectionError(false);
			} catch (err) {
				console.error("Failed to check registration status:", err);
				setHasConnectionError(true);
			} finally {
				setStatusLoading(false);
			}
		};

		if (!isAuthenticated) {
			checkStatus();
		}
	}, [isAuthenticated, checkRegistrationStatus]);

	const isSetupMode = (userCount === 0 || !passwordConfigured) && !hasConnectionError;
	const title = isSetupMode ? "Set Access Password" : "Enter Password";
	const message = useMemo(() => {
		if (hasConnectionError) {
			return "The server did not answer. Check the container or network, then retry.";
		}
		if (isSetupMode) {
			return "Choose one password for this Tater Tube Server.";
		}
		return "Unlock Tater Tube Server.";
	}, [hasConnectionError, isSetupMode]);

	if (isAuthenticated) {
		return null;
	}

	const handlePasswordChange = (value: string) => {
		setPassword(value);
		setLocalError(null);
		clearError();
	};

	const handleConfirmChange = (value: string) => {
		setConfirmPassword(value);
		setLocalError(null);
		clearError();
	};

	const handleSubmit = async (e: React.FormEvent) => {
		e.preventDefault();
		setLocalError(null);

		if (hasConnectionError) {
			return;
		}
		if (!password) {
			setLocalError("Password is required.");
			return;
		}
		if (isSetupMode && password.length < 12) {
			setLocalError("Password must be at least 12 characters.");
			return;
		}
		if (isSetupMode && password !== confirmPassword) {
			setLocalError("Passwords do not match.");
			return;
		}

		const success = isSetupMode
			? await register(password)
			: await login(password);
		if (success) {
			setPassword("");
			setConfirmPassword("");
		}
	};

	const displayedError = localError || error;

	return (
		<div className="relative flex min-h-screen items-center justify-center overflow-hidden bg-base-100 px-4 py-8">
			<div className="pointer-events-none absolute inset-0 bg-[linear-gradient(rgba(255,255,255,0.028)_50%,rgba(0,0,0,0.09)_50%)] bg-[length:100%_4px]" />
			<div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_50%_15%,rgba(255,106,0,0.18),transparent_36%)]" />

			<div className="modal modal-open">
				<div className="modal-box grid w-full max-w-4xl gap-0 overflow-visible border border-primary/25 bg-base-100/95 p-0 shadow-[0_0_60px_rgba(255,106,0,0.12)] md:grid-cols-[minmax(0,1fr)_minmax(220px,0.78fr)]">
					<form onSubmit={handleSubmit} className="space-y-5 p-6 sm:p-8">
						<div className="flex items-center gap-3">
							<div className="flex h-11 w-11 items-center justify-center rounded-md border border-primary/30 bg-primary/10">
								<LockKeyhole className="h-5 w-5 text-primary" aria-hidden="true" />
							</div>
							<div>
								<p className="font-vcr text-[11px] text-primary uppercase">Tater Tube Security</p>
								<h1 className="font-vcr text-2xl text-base-content">{title}</h1>
							</div>
						</div>

						<p className="max-w-md text-base-content/70 text-sm leading-relaxed">{message}</p>

						{statusLoading ? (
							<div className="flex min-h-36 items-center justify-center">
								<span className="loading loading-spinner loading-lg text-primary" />
							</div>
						) : (
							<div className="space-y-4">
								<fieldset className="fieldset">
									<legend className="fieldset-legend">
										{isSetupMode ? "New Password" : "Password"}
									</legend>
									<input
										id="password"
										type="password"
										autoComplete={isSetupMode ? "new-password" : "current-password"}
										required
										value={password}
										onChange={(e) => handlePasswordChange(e.target.value)}
										className="input input-lg w-full border-primary/25 bg-base-200 font-vcr"
										placeholder={isSetupMode ? "Choose a password" : "Enter password"}
										disabled={isLoading || hasConnectionError}
										autoFocus
									/>
								</fieldset>

								{isSetupMode && (
									<fieldset className="fieldset">
										<legend className="fieldset-legend">Confirm Password</legend>
										<input
											id="confirmPassword"
											type="password"
											autoComplete="new-password"
											required
											value={confirmPassword}
											onChange={(e) => handleConfirmChange(e.target.value)}
											className="input input-lg w-full border-primary/25 bg-base-200 font-vcr"
											placeholder="Confirm password"
											disabled={isLoading}
										/>
									</fieldset>
								)}

								{displayedError && (
									<div role="alert" className="alert alert-error border-error/30 bg-error/10 py-3">
										<AlertCircle className="h-5 w-5" aria-hidden="true" />
										<div className="text-sm">{displayedError}</div>
									</div>
								)}

								{hasConnectionError ? (
									<button
										type="button"
										onClick={() => window.location.reload()}
										className="btn btn-primary w-full gap-2 font-vcr"
									>
										<RotateCcw className="h-4 w-4" aria-hidden="true" />
										Retry
									</button>
								) : (
									<button
										type="submit"
										disabled={
											isLoading ||
											!password ||
											(isSetupMode && !confirmPassword)
										}
										className="btn btn-primary w-full gap-2 font-vcr"
									>
										{isLoading ? (
											<>
												<span className="loading loading-spinner loading-sm" />
												Please Wait
											</>
										) : (
											<>
												<ShieldCheck className="h-4 w-4" aria-hidden="true" />
												{isSetupMode ? "Save Password" : "Unlock"}
											</>
										)}
									</button>
								)}
							</div>
						)}
					</form>

					<div className="relative hidden min-h-[420px] items-end justify-center overflow-hidden rounded-r-box border-primary/10 border-l bg-base-200/60 md:flex">
						<div className="absolute inset-0 bg-[radial-gradient(circle_at_50%_22%,rgba(255,106,0,0.16),transparent_42%)]" />
						<img
							src="/tater-security-mascot.png"
							alt="Tater Tube security mascot"
							className="relative z-10 max-h-[450px] w-full object-contain object-bottom px-4 pt-6"
						/>
					</div>
				</div>
			</div>
		</div>
	);
}
