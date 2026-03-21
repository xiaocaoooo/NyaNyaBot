import { Suspense } from "react";

import { LoginScreen } from "@/components/screens/login-screen";

export default function LoginPage() {
  return (
    <Suspense fallback={null}>
      <LoginScreen />
    </Suspense>
  );
}
