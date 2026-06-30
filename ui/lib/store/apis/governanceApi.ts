import {
	BulkRotateVirtualKeysRequest,
	BulkRotateVirtualKeysResponse,
	CreateVirtualKeyRequest,
	CreatePricingOverrideRequest,
	GetVirtualKeysParams,
	GetVirtualKeysResponse,
	GetPricingOverridesResponse,
	PricingOverride,
	PricingOverrideScopeKind,
	UpdatePricingOverrideRequest,
	UpdateVirtualKeyRequest,
	VirtualKey,
} from "@/lib/types/governance";
import { baseApi } from "./baseApi";

type PricingOverrideQueryArgs = {
	scopeKind?: PricingOverrideScopeKind;
	virtualKeyID?: string;
	providerID?: string;
	providerKeyID?: string;
	limit?: number;
	offset?: number;
	search?: string;
};

export const governanceApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getVirtualKeys: builder.query<GetVirtualKeysResponse, GetVirtualKeysParams | void>({
			query: (params) => ({
				url: "/governance/virtual-keys",
				params: {
					...(params?.limit && { limit: params.limit }),
					...(params?.offset !== undefined && { offset: params.offset }),
					...(params?.search && { search: params.search }),
					...(params?.sort_by && { sort_by: params.sort_by }),
					...(params?.order && { order: params.order }),
					...(params?.export && { export: "true" }),
				},
			}),
			providesTags: ["VirtualKeys"],
		}),

		getVirtualKey: builder.query<{ virtual_key: VirtualKey }, string>({
			query: (vkId) => `/governance/virtual-keys/${vkId}`,
			providesTags: (result, error, vkId) => [{ type: "VirtualKeys", id: vkId }],
		}),

		createVirtualKey: builder.mutation<{ message: string; virtual_key: VirtualKey }, CreateVirtualKeyRequest>({
			query: (data) => ({
				url: "/governance/virtual-keys",
				method: "POST",
				body: data,
			}),
			invalidatesTags: ["VirtualKeys"],
		}),

		updateVirtualKey: builder.mutation<{ message: string; virtual_key: VirtualKey }, { vkId: string; data: UpdateVirtualKeyRequest }>({
			query: ({ vkId, data }) => ({
				url: `/governance/virtual-keys/${vkId}`,
				method: "PUT",
				body: data,
			}),
			invalidatesTags: ["VirtualKeys"],
		}),

		rotateVirtualKey: builder.mutation<{ message: string; virtual_key: VirtualKey }, string>({
			query: (vkId) => ({
				url: `/governance/virtual-keys/${vkId}/rotate`,
				method: "POST",
			}),
			invalidatesTags: ["VirtualKeys"],
		}),

		bulkRotateVirtualKeys: builder.mutation<BulkRotateVirtualKeysResponse, BulkRotateVirtualKeysRequest>({
			query: (data) => ({
				url: "/governance/virtual-keys/rotate",
				method: "POST",
				body: data,
			}),
			invalidatesTags: ["VirtualKeys"],
		}),

		deleteVirtualKey: builder.mutation<{ message: string }, string>({
			query: (vkId) => ({
				url: `/governance/virtual-keys/${vkId}`,
				method: "DELETE",
			}),
			invalidatesTags: ["VirtualKeys"],
		}),

		getPricingOverrides: builder.query<GetPricingOverridesResponse, PricingOverrideQueryArgs | void>({
			query: (params) => ({
				url: "/governance/pricing-overrides",
				params: {
					...(params?.scopeKind && { scope_kind: params.scopeKind }),
					...(params?.virtualKeyID && { virtual_key_id: params.virtualKeyID }),
					...(params?.providerID && { provider_id: params.providerID }),
					...(params?.providerKeyID && { provider_key_id: params.providerKeyID }),
					...(params?.limit !== undefined && { limit: params.limit }),
					...(params?.offset !== undefined && { offset: params.offset }),
					...(params?.search && { search: params.search }),
				},
			}),
			providesTags: ["PricingOverrides"],
		}),

		createPricingOverride: builder.mutation<{ message: string; pricing_override: PricingOverride }, CreatePricingOverrideRequest>({
			query: (data) => ({
				url: "/governance/pricing-overrides",
				method: "POST",
				body: data,
			}),
			invalidatesTags: ["PricingOverrides"],
		}),

		updatePricingOverride: builder.mutation<
			{ message: string; pricing_override: PricingOverride },
			{ id: string; data: UpdatePricingOverrideRequest }
		>({
			query: ({ id, data }) => ({
				url: `/governance/pricing-overrides/${id}`,
				method: "PUT",
				body: data,
			}),
			invalidatesTags: ["PricingOverrides"],
		}),

		deletePricingOverride: builder.mutation<{ message: string }, string>({
			query: (id) => ({
				url: `/governance/pricing-overrides/${id}`,
				method: "DELETE",
			}),
			invalidatesTags: ["PricingOverrides"],
		}),
	}),
});

export const {
	useGetVirtualKeysQuery,
	useGetVirtualKeyQuery,
	useCreateVirtualKeyMutation,
	useUpdateVirtualKeyMutation,
	useRotateVirtualKeyMutation,
	useBulkRotateVirtualKeysMutation,
	useDeleteVirtualKeyMutation,
	useGetPricingOverridesQuery,
	useCreatePricingOverrideMutation,
	useUpdatePricingOverrideMutation,
	useDeletePricingOverrideMutation,
	useLazyGetVirtualKeysQuery,
	useLazyGetVirtualKeyQuery,
} = governanceApi;
