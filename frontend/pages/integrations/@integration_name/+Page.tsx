import { usePageContext } from "vike-react/usePageContext";
import { navigate } from "vike/client/router";
import { IntegrationPageShell } from "@open-chat-go/ui";
import { IntegrationOverviewView } from "@open-chat-go/ui";

export default function IntegrationDetailsPage() {
  const pageContext = usePageContext();
  const integrationName = String(pageContext.routeParams.integration_name || "");

  return (
    <IntegrationPageShell>
      <IntegrationOverviewView
        integrationName={integrationName}
        navigateTo={(to: string) => navigate(to)}
      />
    </IntegrationPageShell>
  );
}
