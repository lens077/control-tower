import { proxy } from "valtio";
import { decodeJwtPayload, subscribeToken } from "@/auth";

export interface Account {
  owner?: string;
  name?: string;
  avatar?: string;
  email?: string;
  id?: string;
  role?: string;
  displayName?: string;
  isDeleted?: string;
  createdTime?: string;
  updatedTime?: string;
}

export interface UserState {
  account: Account;
}

export const EMPTY_ACCOUNT: Account = {
  owner: "",
  name: "",
  avatar: "",
  email: "",
  id: "",
  role: "",
  displayName: "",
};

export const userStore = proxy<UserState>({ account: { ...EMPTY_ACCOUNT } });

export const setAccount = (account: Partial<Account>) => {
  userStore.account = { ...userStore.account, ...account };
};

export const clearAccount = () => {
  userStore.account = { ...EMPTY_ACCOUNT };
};

subscribeToken((token) => {
  if (!token) {
    clearAccount();
    return;
  }
  const claims = decodeJwtPayload(token);
  if (!claims) return;
  setAccount({
    id: claims.id ?? claims.sub,
    name: claims.name,
    displayName: claims.displayName,
    email: claims.email,
    avatar: claims.avatar,
    role: claims.role,
  });
});
