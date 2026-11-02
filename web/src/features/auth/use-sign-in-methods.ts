import { useQuery } from "@tanstack/react-query";

export interface SignInMethods {
  password: boolean;
  signup: boolean;
  forgot_password: boolean;
  providers: string[];
}

export function useSignInMethods(): SignInMethods | undefined {
  const { data } = useQuery({
    queryKey: ["capabilities", "sign-in"],
    queryFn: async () => {
      const res = await fetch("/api/capabilities");
      if (!res.ok) throw new Error("Failed to load sign-in methods");
      const body = (await res.json()) as { sign_in?: SignInMethods };
      return body.sign_in ?? { password: true, signup: true, forgot_password: false, providers: [] };
    },
  });
  return data;
}
