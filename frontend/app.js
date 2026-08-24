async function refreshIncidents(){const response=await fetch('/api/incidents');return response.json();}
