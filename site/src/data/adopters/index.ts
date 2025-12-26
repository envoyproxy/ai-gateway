import adoptersData from './adopters.json';

export type Adopter = {
  name: string;
  logoUrl: string;
  url?: string;
  description?: string;
};

// Import adopters from the consolidated JSON file
export const adopters: Adopter[] = adoptersData as Adopter[];

// Sort adopters alphabetically by name
export const sortedAdopters: Adopter[] = [...adopters].sort((a, b) => a.name.localeCompare(b.name));
