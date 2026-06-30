import { Button } from "@/components/ui/button";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { DefaultPerformanceConfig } from "@/lib/constants/config";
import { getErrorMessage, setProviderFormDirtyState, useAppDispatch } from "@/lib/store";
import { useUpdateProviderMutation } from "@/lib/store/apis/providersApi";
import { ModelProvider } from "@/lib/types/config";
import { performanceFormSchema, type PerformanceFormSchema } from "@/lib/types/schemas";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { zodResolver } from "@hookform/resolvers/zod";
import { useEffect } from "react";
import { useForm, type Resolver } from "react-hook-form";
import { toast } from "sonner";
import { buildProviderUpdatePayload } from "../views/utils";

interface PerformanceFormFragmentProps {
	provider: ModelProvider;
}

function getPriceRMBPerDao(description?: string): number | undefined {
	if (!description) return undefined;
	try {
		const value = JSON.parse(description).price_rmb_per_dao;
		return typeof value === "number" && Number.isFinite(value) ? value : undefined;
	} catch {
		return undefined;
	}
}

function setPriceRMBPerDao(description: string | undefined, price: number | undefined): string {
	let metadata: Record<string, unknown> = {};
	if (description) {
		try {
			const parsed = JSON.parse(description);
			if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) metadata = parsed;
		} catch {}
	}
	if (price == null) delete metadata.price_rmb_per_dao;
	else metadata.price_rmb_per_dao = price;
	return Object.keys(metadata).length ? JSON.stringify(metadata) : "";
}

export function PerformanceFormFragment({ provider }: PerformanceFormFragmentProps) {
	const dispatch = useAppDispatch();
	const hasUpdateProviderAccess = useRbac(RbacResource.ModelProvider, RbacOperation.Update);
	const [updateProvider, { isLoading: isUpdatingProvider }] = useUpdateProviderMutation();
	const form = useForm<PerformanceFormSchema, any, PerformanceFormSchema>({
		resolver: zodResolver(performanceFormSchema) as Resolver<PerformanceFormSchema, any, PerformanceFormSchema>,
		mode: "onChange",
		reValidateMode: "onChange",
		defaultValues: {
			concurrency_and_buffer_size: {
				concurrency: provider.concurrency_and_buffer_size?.concurrency ?? DefaultPerformanceConfig.concurrency,
				buffer_size: provider.concurrency_and_buffer_size?.buffer_size ?? DefaultPerformanceConfig.buffer_size,
			},
			price_rmb_per_dao: getPriceRMBPerDao(provider.description),
		},
	});

	useEffect(() => {
		dispatch(setProviderFormDirtyState(form.formState.isDirty));
	}, [form.formState.isDirty]);

	useEffect(() => {
		// Reset form with new provider's concurrency_and_buffer_size when provider changes
		form.reset({
			concurrency_and_buffer_size: {
				concurrency: provider.concurrency_and_buffer_size?.concurrency ?? DefaultPerformanceConfig.concurrency,
				buffer_size: provider.concurrency_and_buffer_size?.buffer_size ?? DefaultPerformanceConfig.buffer_size,
			},
			price_rmb_per_dao: getPriceRMBPerDao(provider.description),
		});
	}, [form, provider.name, provider.concurrency_and_buffer_size, provider.description]);

	const onSubmit = (data: PerformanceFormSchema) => {
		// Create updated provider configuration (raw request/response are in Debugging tab)
		const updatedProvider = buildProviderUpdatePayload(provider, {
			concurrency_and_buffer_size: {
				concurrency: data.concurrency_and_buffer_size.concurrency,
				buffer_size: data.concurrency_and_buffer_size.buffer_size,
			},
			description: setPriceRMBPerDao(provider.description, data.price_rmb_per_dao),
		});
		updateProvider(updatedProvider)
			.unwrap()
			.then(() => {
				toast.success("Provider 配置已更新");
				form.reset(data);
			})
			.catch((err) => {
				toast.error("更新 Provider 配置失败", {
					description: getErrorMessage(err),
				});
			});
	};

	return (
		<Form {...form}>
			<form onSubmit={form.handleSubmit(onSubmit)} className="space-y-6 px-6">
				{/* Performance Configuration */}
				<div className="space-y-4">
					<div className="flex flex-row gap-4">
						<div className="flex-1">
							<FormField
								control={form.control}
								name="concurrency_and_buffer_size.concurrency"
								render={({ field }) => (
									<FormItem>
										<FormLabel>Concurrency</FormLabel>
										<FormControl>
											<Input
												type="number"
												placeholder="10"
												{...field}
												value={field.value === undefined || Number.isNaN(field.value) ? "" : field.value}
												disabled={!hasUpdateProviderAccess}
												onChange={(e) => {
													const value = e.target.value;
													if (value === "") {
														field.onChange(undefined);
														return;
													}
													const parsed = Number.parseInt(value);
													if (!Number.isNaN(parsed)) {
														field.onChange(parsed);
													}
													form.trigger("concurrency_and_buffer_size");
												}}
											/>
										</FormControl>
										<FormMessage />
									</FormItem>
								)}
							/>
						</div>
						<div className="flex-1">
							<FormField
								control={form.control}
								name="concurrency_and_buffer_size.buffer_size"
								render={({ field }) => (
									<FormItem>
										<FormLabel>Buffer Size</FormLabel>
										<FormControl>
											<Input
												type="number"
												placeholder="10"
												{...field}
												value={field.value === undefined || Number.isNaN(field.value) ? "" : field.value}
												disabled={!hasUpdateProviderAccess}
												onChange={(e) => {
													const value = e.target.value;
													if (value === "") {
														field.onChange(undefined);
														return;
													}
													const parsed = Number.parseInt(value);
													if (!Number.isNaN(parsed)) {
														field.onChange(parsed);
													}
													form.trigger("concurrency_and_buffer_size");
												}}
											/>
										</FormControl>
										<FormMessage />
									</FormItem>
								)}
							/>
						</div>
					</div>
					<FormField
						control={form.control}
						name="price_rmb_per_dao"
						render={({ field }) => (
							<FormItem>
								<FormLabel>渠道成本（RMB / 1刀额度）</FormLabel>
								<FormControl>
									<Input
										type="number"
										min="0"
										step="any"
										placeholder="0.1"
										value={field.value === undefined || Number.isNaN(field.value) ? "" : field.value}
										disabled={!hasUpdateProviderAccess}
										onChange={(e) => {
											const value = e.target.value;
											if (value === "") {
												field.onChange(undefined);
												return;
											}
											const parsed = Number(value);
											if (!Number.isNaN(parsed)) field.onChange(parsed);
										}}
									/>
								</FormControl>
								<FormMessage />
							</FormItem>
						)}
					/>
				</div>

				{/* Form Actions */}
				<div className="mb-6 flex justify-end space-x-2">
					<Button
						type="submit"
						disabled={!form.formState.isDirty || !hasUpdateProviderAccess || isUpdatingProvider}
						isLoading={isUpdatingProvider}
					>
						保存性能配置
					</Button>
				</div>
			</form>
		</Form>
	);
}
