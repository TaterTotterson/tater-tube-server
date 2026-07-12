import { AlertTriangle, KeyRound, ShieldCheck, Trash2, UserCheck } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import { useAuth } from "../../hooks/useAuth";
import type { AuthConfig, ConfigResponse } from "../../types/config";
import { LoadingSpinner } from "../ui/LoadingSpinner";

interface PasswordForm {
	password: string;
	confirmPassword: string;
}

interface ChangePasswordForm {
	currentPassword: string;
	newPassword: string;
	confirmPassword: string;
}

interface RegistrationStatus {
	registration_enabled: boolean;
	setup_required: boolean;
	password_configured: boolean;
	user_count: number;
}

interface AuthConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: AuthConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

export function AuthConfigSection({ isReadOnly = false }: AuthConfigSectionProps) {
	const { user, recheckAuth } = useAuth();
	const [registrationStatus, setRegistrationStatus] = useState<RegistrationStatus | null>(null);
	const [createForm, setCreateForm] = useState<PasswordForm>({
		password: "",
		confirmPassword: "",
	});
	const [createError, setCreateError] = useState<string | null>(null);
	const [createSuccess, setCreateSuccess] = useState(false);
	const [isCreatingPassword, setIsCreatingPassword] = useState(false);

	const [changeForm, setChangeForm] = useState<ChangePasswordForm>({
		currentPassword: "",
		newPassword: "",
		confirmPassword: "",
	});
	const [changeError, setChangeError] = useState<string | null>(null);
	const [changeSuccess, setChangeSuccess] = useState(false);
	const [isChangingPassword, setIsChangingPassword] = useState(false);
	const [clearError, setClearError] = useState<string | null>(null);
	const [isClearingPassword, setIsClearingPassword] = useState(false);

	const fetchRegistrationStatus = useCallback(async () => {
		try {
			const status = await apiClient.checkRegistrationStatus();
			setRegistrationStatus(status);
		} catch {
			setRegistrationStatus(null);
		}
	}, []);

	useEffect(() => {
		void fetchRegistrationStatus();
	}, [fetchRegistrationStatus]);

	const validatePasswordForm = (form: PasswordForm): string | null => {
		if (form.password.length < 12) {
			return "Password must be at least 12 characters.";
		}
		if (form.password !== form.confirmPassword) {
			return "Passwords do not match.";
		}
		return null;
	};

	const handleCreatePassword = async () => {
		const validationError = validatePasswordForm(createForm);
		if (validationError) {
			setCreateError(validationError);
			return;
		}

		setIsCreatingPassword(true);
		setCreateError(null);
		setCreateSuccess(false);
		try {
			await apiClient.register(createForm.password);
			setCreateForm({ password: "", confirmPassword: "" });
			setCreateSuccess(true);
			await fetchRegistrationStatus();
			await recheckAuth();
		} catch (err) {
			setCreateError(err instanceof Error ? err.message : "Failed to set password.");
		} finally {
			setIsCreatingPassword(false);
		}
	};

	const handleChangePassword = async () => {
		if (changeForm.newPassword.length < 12) {
			setChangeError("New password must be at least 12 characters.");
			return;
		}
		if (changeForm.newPassword !== changeForm.confirmPassword) {
			setChangeError("Passwords do not match.");
			return;
		}

		setIsChangingPassword(true);
		setChangeError(null);
		setChangeSuccess(false);
		try {
			await apiClient.changeOwnPassword({
				current_password: changeForm.currentPassword,
				new_password: changeForm.newPassword,
			});
			setChangeSuccess(true);
			setChangeForm({ currentPassword: "", newPassword: "", confirmPassword: "" });
			await fetchRegistrationStatus();
			await recheckAuth();
		} catch (err) {
			setChangeError(err instanceof Error ? err.message : "Failed to update password.");
		} finally {
			setIsChangingPassword(false);
		}
	};

	const handleClearPassword = async () => {
		setIsClearingPassword(true);
		setClearError(null);
		setChangeSuccess(false);
		try {
			await apiClient.clearServerPassword();
			setChangeForm({ currentPassword: "", newPassword: "", confirmPassword: "" });
			await fetchRegistrationStatus();
			await recheckAuth();
		} catch (err) {
			setClearError(err instanceof Error ? err.message : "Failed to remove password.");
		} finally {
			setIsClearingPassword(false);
		}
	};

	const passwordConfigured = registrationStatus?.password_configured === true;
	const passwordUser = user?.provider === "password" || user?.provider === "direct";

	return (
		<div className="space-y-8">
			<div className="space-y-6 rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="flex items-center gap-2">
					<ShieldCheck className="h-4 w-4 text-base-content/60" />
					<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
						Server Password
					</h4>
					<div className="h-px flex-1 bg-base-300/50" />
				</div>

				{registrationStatus === null ? (
					<div className="flex min-h-32 items-center justify-center">
						<LoadingSpinner size="lg" />
					</div>
				) : passwordConfigured ? (
					<div className="space-y-4 rounded-xl border-2 border-success/20 bg-success/5 p-4">
						<div className="flex items-center gap-2">
							<UserCheck className="h-4 w-4 text-success" />
							<span className="font-bold text-success text-xs uppercase tracking-widest">
								Password Set
							</span>
						</div>

						<p className="text-[11px] text-base-content/60 leading-relaxed">
							Login is enabled because this server has a password.
						</p>

						{passwordUser ? (
							<>
								<fieldset className="fieldset">
									<legend className="fieldset-legend">Current Password</legend>
									<input
										type="password"
										className="input w-full"
										placeholder="Enter current password"
										value={changeForm.currentPassword}
										disabled={isReadOnly || isChangingPassword}
										onChange={(e) =>
											setChangeForm((form) => ({
												...form,
												currentPassword: e.target.value,
											}))
										}
									/>
								</fieldset>

								<fieldset className="fieldset">
									<legend className="fieldset-legend">New Password</legend>
									<input
										type="password"
										className="input w-full"
										placeholder="Min. 12 characters"
										value={changeForm.newPassword}
										disabled={isReadOnly || isChangingPassword}
										onChange={(e) =>
											setChangeForm((form) => ({
												...form,
												newPassword: e.target.value,
											}))
										}
									/>
								</fieldset>

								<fieldset className="fieldset">
									<legend className="fieldset-legend">Confirm New Password</legend>
									<input
										type="password"
										className="input w-full"
										placeholder="Repeat new password"
										value={changeForm.confirmPassword}
										disabled={isReadOnly || isChangingPassword}
										onChange={(e) =>
											setChangeForm((form) => ({
												...form,
												confirmPassword: e.target.value,
											}))
										}
									/>
								</fieldset>

								{changeError && (
									<div className="alert alert-error items-start rounded-xl px-4 py-3">
										<AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
										<span className="text-[11px]">{changeError}</span>
									</div>
								)}

								{clearError && (
									<div className="alert alert-error items-start rounded-xl px-4 py-3">
										<AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
										<span className="text-[11px]">{clearError}</span>
									</div>
								)}

								{changeSuccess && (
									<div className="alert alert-success items-start rounded-xl px-4 py-3">
										<UserCheck className="mt-0.5 h-4 w-4 shrink-0" />
										<span className="text-[11px]">Password updated.</span>
									</div>
								)}

								{!isReadOnly && (
									<div className="flex flex-wrap justify-end gap-2">
										<button
											type="button"
											className="btn btn-sm btn-error"
											onClick={handleClearPassword}
											disabled={isChangingPassword || isClearingPassword}
										>
											{isClearingPassword ? (
												<LoadingSpinner size="sm" />
											) : (
												<Trash2 className="h-3 w-3" />
											)}
											{isClearingPassword ? "Removing..." : "Remove Password"}
										</button>
										<button
											type="button"
											className="btn btn-sm btn-success"
											onClick={handleChangePassword}
											disabled={
												isChangingPassword ||
												isClearingPassword ||
												!changeForm.currentPassword ||
												!changeForm.newPassword ||
												!changeForm.confirmPassword
											}
										>
											{isChangingPassword ? (
												<LoadingSpinner size="sm" />
											) : (
												<KeyRound className="h-3 w-3" />
											)}
											{isChangingPassword ? "Updating..." : "Update Password"}
										</button>
									</div>
								)}
							</>
						) : (
							<div className="alert items-start rounded-xl border border-info/20 bg-info/5 px-4 py-3">
								<ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-info" />
								<span className="text-[11px]">
									Log in with the server password to change it.
								</span>
							</div>
						)}
					</div>
				) : (
					<div className="space-y-4 rounded-xl border-2 border-primary/20 bg-primary/5 p-4">
						<div className="flex items-center gap-2">
							<KeyRound className="h-4 w-4 text-primary" />
							<span className="font-bold text-primary text-xs uppercase tracking-widest">
								No Password Set
							</span>
						</div>

						<p className="text-[11px] text-base-content/60 leading-relaxed">
							The web UI is open. Set a password here to enable login.
						</p>

						<fieldset className="fieldset">
							<legend className="fieldset-legend">Password</legend>
							<input
								type="password"
								className="input w-full"
								placeholder="Min. 12 characters"
								value={createForm.password}
								disabled={isReadOnly || isCreatingPassword}
								onChange={(e) =>
									setCreateForm((form) => ({ ...form, password: e.target.value }))
								}
							/>
						</fieldset>

						<fieldset className="fieldset">
							<legend className="fieldset-legend">Confirm Password</legend>
							<input
								type="password"
								className="input w-full"
								placeholder="Repeat password"
								value={createForm.confirmPassword}
								disabled={isReadOnly || isCreatingPassword}
								onChange={(e) =>
									setCreateForm((form) => ({
										...form,
										confirmPassword: e.target.value,
									}))
								}
							/>
						</fieldset>

						{createError && (
							<div className="alert alert-error items-start rounded-xl px-4 py-3">
								<AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
								<span className="text-[11px]">{createError}</span>
							</div>
						)}

						{createSuccess && (
							<div className="alert alert-success items-start rounded-xl px-4 py-3">
								<UserCheck className="mt-0.5 h-4 w-4 shrink-0" />
								<span className="text-[11px]">Password set. Login is now enabled.</span>
							</div>
						)}

						{!isReadOnly && (
							<div className="flex justify-end">
								<button
									type="button"
									className="btn btn-sm btn-primary"
									onClick={handleCreatePassword}
									disabled={
										isCreatingPassword ||
										!createForm.password ||
										!createForm.confirmPassword
									}
								>
									{isCreatingPassword ? (
										<LoadingSpinner size="sm" />
									) : (
										<KeyRound className="h-3 w-3" />
									)}
									{isCreatingPassword ? "Saving..." : "Set Password"}
								</button>
							</div>
						)}
					</div>
				)}
			</div>
		</div>
	);
}
