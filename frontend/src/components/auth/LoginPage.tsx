import { AlertCircle, LockKeyhole, RotateCcw, ShieldCheck } from "lucide-react";
import type React from "react";
import { useState } from "react";
import { useAuth } from "../../hooks/useAuth";

export function LoginPage() {
	const {
		isAuthenticated,
		login,
		isLoading,
		error,
		clearError,
	} = useAuth();
	const [hasConnectionError, setHasConnectionError] = useState(false);
	const [password, setPassword] = useState("");
	const [localError, setLocalError] = useState<string | null>(null);

	const title = "Enter Password";
	const message = hasConnectionError
		? "The server did not answer. Check the container or network, then retry."
		: "Unlock Tater Tube Server.";

	if (isAuthenticated) {
		return null;
	}

	const handlePasswordChange = (value: string) => {
		setPassword(value);
		setHasConnectionError(false);
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

		const success = await login(password);
		if (success) {
			setPassword("");
			setHasConnectionError(false);
			return;
		}
		if (!error) {
			setHasConnectionError(false);
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

						<div className="space-y-4">
							<fieldset className="fieldset">
								<legend className="fieldset-legend">Password</legend>
								<input
									id="password"
									type="password"
									autoComplete="current-password"
									required
									value={password}
									onChange={(e) => handlePasswordChange(e.target.value)}
									className="input input-lg w-full border-primary/25 bg-base-200 font-vcr"
									placeholder="Enter password"
									disabled={isLoading || hasConnectionError}
									autoFocus
								/>
							</fieldset>

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
									disabled={isLoading || !password}
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
											Unlock
										</>
									)}
								</button>
							)}
						</div>
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
