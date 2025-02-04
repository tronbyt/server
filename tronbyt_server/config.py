# import os
# from dotenv import load_dotenv

# if(load_dotenv()):
#     print("Loaded .env file")
# else:
#     print("No .env file found")


# # change this for production environment
# # if there is no ENV variable set for domain_name then set it here
# if 'SERVER_HOSTNAME' in os.environ:
#     domain_host = os.environ['SERVER_HOSTNAME']
#     print("Using domain from environment variable : {}".format(domain_host))
# else:
#     domain_host = "localhost"
#     print("Using default from config.py : {}".format(domain_host))